// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func New(root, run, listen, version string, dev bool) (*App, error) {
	return NewWithStorage(root, run, listen, version, dev, StorageNative)
}

func NewWithStorage(root, run, listen, version string, dev bool, storageMode string) (*App, error) {
	if storageMode != StorageNative && storageMode != StorageUSB {
		return nil, fmt.Errorf("存储模式无效")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return nil, fmt.Errorf("IPC 必须监听回环地址")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	run, err = filepath.Abs(run)
	if err != nil {
		return nil, err
	}
	a := &App{root: root, run: run, listen: listen, version: version, dev: dev, storageMode: storageMode, state: defaults(), jobs: map[string]*Job{}, uploads: map[string]*Upload{}, downloads: map[string]string{}, started: time.Now()}
	if storageMode == StorageUSB && !dev {
		a.usb, err = inspectUSBStorage(root)
		if err != nil {
			return nil, err
		}
	}
	for _, dir := range []string{root, run, filepath.Join(root, "profiles"), filepath.Join(root, "cores"), filepath.Join(a.coreDataDir(), "providers"), filepath.Join(run, "transfers")} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
	}
	if dev {
		a.state.Settings.MixedPort = 18990
		a.state.Settings.RedirPort = 18992
		a.state.Settings.TProxyPort = 18993
		a.state.Settings.DNSPort = 11054
		a.state.Settings.ControllerPort = 19990
	}
	if b, err := os.ReadFile(filepath.Join(root, "state.json")); err == nil {
		if err = json.Unmarshal(b, &a.state); err != nil {
			return nil, fmt.Errorf("配置文件损坏，未覆盖原文件：%w", err)
		}
		if a.state.Schema != 1 {
			return nil, fmt.Errorf("不支持的配置版本 %d", a.state.Schema)
		}
		if err = validateSettings(a.state.Settings); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	a.token = randomID(32)
	secretPath := filepath.Join(root, "controller.secret")
	if b, err := os.ReadFile(secretPath); err == nil && len(b) >= 32 {
		a.secret = strings.TrimSpace(string(b))
	} else {
		a.secret = randomID(32)
		if err = atomicWrite(secretPath, []byte(a.secret), 0600); err != nil {
			return nil, err
		}
	}
	// Register a first kernel, or move an identical kernel to USB raw storage.
	if a.state.Kernel.File == "" || storageMode == StorageUSB {
		if b, err := os.ReadFile(filepath.Join(root, "bundled-kernel.json")); err == nil {
			var k KernelState
			if err = json.Unmarshal(b, &k); err != nil {
				return nil, err
			}
			if !validID(k.File) {
				return nil, fmt.Errorf("内核清单路径无效")
			}
			if a.state.Kernel.File == "" {
				if err = verifyStoredCore(filepath.Join(root, "cores", k.File), k); err != nil {
					return nil, err
				}
				a.state.Kernel = k
			} else if a.state.Kernel.SHA256 == k.SHA256 && a.state.Kernel.Version == k.Version {
				if err = verifyStoredCore(filepath.Join(root, "cores", k.File), k); err != nil {
					return nil, err
				}
				k.Previous = a.state.Kernel.Previous
				a.state.Kernel = k
			}
		}
	}
	if err = a.persist(a.state); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) Serve(parent context.Context) error {
	listener, err := net.Listen("tcp", a.listen)
	if err != nil {
		return err
	}
	a.ctx, a.cancel = context.WithCancel(parent)
	defer a.cancel()
	defer listener.Close()
	if a.usb != nil {
		binding, _ := json.Marshal(a.usb)
		if err = atomicWrite(filepath.Join(a.run, "storage-binding.json"), binding, 0600); err != nil {
			return err
		}
	} else {
		_ = os.Remove(filepath.Join(a.run, "storage-binding.json"))
	}
	addr := listener.Addr().String()
	info, _ := json.Marshal(map[string]string{"address": addr, "token": a.token})
	if err = atomicWrite(filepath.Join(a.run, "ipc.json"), info, 0600); err != nil {
		return err
	}
	defer os.Remove(filepath.Join(a.run, "ipc.json"))
	server := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 95 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	if !a.dev {
		if err = a.cleanupNetwork(); err != nil {
			a.log("warning", "启动时清理旧接管规则："+err.Error())
		}
	}
	a.log("info", "长城 Clash 管理程序已启动 "+a.version)
	go a.background()
	go func() {
		<-a.ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()
	if a.snapshot().Enabled {
		a.newJob("autostart", func() (any, error) {
			a.op.Lock()
			defer a.op.Unlock()
			err := a.startService(false)
			if err != nil {
				a.setFailure("自动启动失败：" + err.Error())
			}
			return nil, err
		})
	}
	err = server.Serve(listener)
	a.op.Lock()
	_ = a.stopService(false)
	a.op.Unlock()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (a *App) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CWClash-Token")), []byte(a.token)) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(Envelope{Error: "需要本机认证"})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/rpc" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var request struct {
			Action  string          `json:"action"`
			Payload json.RawMessage `json:"payload"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
		if err := decoder.Decode(&request); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(Envelope{Error: "请求格式无效"})
			return
		}
		if len(request.Payload) == 0 {
			request.Payload = json.RawMessage(`{}`)
		}
		result, err := a.dispatch(r.Context(), request.Action, request.Payload)
		response := Envelope{OK: err == nil, Data: result}
		if err != nil {
			response.Data = nil
			response.Error = redact(err.Error())
		}
		_ = json.NewEncoder(w).Encode(response)
	})
}

func Call(run, action, payload string) Envelope {
	b, err := os.ReadFile(filepath.Join(run, "ipc.json"))
	if err != nil {
		return Envelope{Error: "管理服务未运行，请从软件中心重新启动插件管理服务"}
	}
	var ipc struct {
		Address string `json:"address"`
		Token   string `json:"token"`
	}
	if json.Unmarshal(b, &ipc) != nil {
		return Envelope{Error: "管理服务地址无效"}
	}
	host, _, err := net.SplitHostPort(ipc.Address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return Envelope{Error: "拒绝访问非本机管理地址"}
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(decoded) > 192<<10 || !json.Valid(decoded) {
		return Envelope{Error: "请求参数无效或过大"}
	}
	body, _ := json.Marshal(map[string]any{"action": action, "payload": json.RawMessage(decoded)})
	req, _ := http.NewRequest("POST", "http://"+ipc.Address+"/rpc", bytes.NewReader(body))
	req.Header.Set("X-CWClash-Token", ipc.Token)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 95 * time.Second, Transport: &http.Transport{Proxy: nil}}
	res, err := client.Do(req)
	if err != nil {
		return Envelope{Error: "管理服务连接失败"}
	}
	defer res.Body.Close()
	var result Envelope
	if json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&result) != nil {
		return Envelope{Error: "管理服务返回无效数据"}
	}
	return result
}

func (a *App) newJob(kind string, fn func() (any, error)) map[string]string {
	id := randomID(12)
	job := &Job{ID: id, Kind: kind, Status: "running", StartedAt: time.Now().Format(time.RFC3339), Message: "处理中"}
	a.mu.Lock()
	if len(a.jobs) > 80 {
		for key, j := range a.jobs {
			if j.Status != "running" {
				delete(a.jobs, key)
			}
		}
	}
	a.jobs[id] = job
	a.mu.Unlock()
	go func() {
		defer func() {
			if v := recover(); v != nil {
				a.finishJob(id, nil, fmt.Errorf("操作中断：%v", v))
			}
		}()
		data, err := fn()
		a.finishJob(id, data, err)
	}()
	return map[string]string{"jobId": id}
}

func (a *App) finishJob(id string, data any, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if j := a.jobs[id]; j != nil {
		j.FinishedAt = time.Now().Format(time.RFC3339)
		j.Result = data
		j.Status = "done"
		j.Message = "已完成"
		if err != nil {
			j.Status = "error"
			j.Message = redact(err.Error())
		}
	}
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

var secretURLPattern = regexp.MustCompile(`(?i)(https?://)[^\s"'<>]+`)
var inlineSecretPattern = regexp.MustCompile(`(?i)(password|passwd|token|secret|authorization|uuid)([=: ]+)[^\s,;]+`)

func redact(s string) string {
	s = secretURLPattern.ReplaceAllStringFunc(s, func(v string) string {
		if i := strings.Index(v, "://"); i >= 0 {
			rest := v[i+3:]
			if slash := strings.IndexAny(rest, "/?#"); slash >= 0 {
				rest = rest[:slash] + "/[已隐藏]"
				v = v[:i+3] + rest
			}
			if at := strings.Index(rest, "@"); at >= 0 {
				v = v[:i+3] + "[已隐藏]@" + rest[at+1:]
			}
		}
		return v
	})
	return inlineSecretPattern.ReplaceAllString(s, "$1$2[已隐藏]")
}
