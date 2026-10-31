// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Core struct {
	mu      sync.RWMutex
	cmd     *exec.Cmd
	done    chan struct{}
	running bool
	err     error
}

var controllerHTTPClient = &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConns: 8, MaxIdleConnsPerHost: 4, IdleConnTimeout: 30 * time.Second}, Timeout: 20 * time.Second}

func (c *Core) Running() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.running
}
func (c *Core) Memory() int64 {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", c.cmd.Process.Pid))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			parts := strings.Fields(line)
			if len(parts) > 1 {
				n, _ := strconv.ParseInt(parts[1], 10, 64)
				return n * 1024
			}
		}
	}
	return 0
}

func (a *App) isRunning() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.core != nil && a.core.Running()
}

func verifyFile(path, expected string) error {
	if len(expected) != 64 {
		return fmt.Errorf("缺少有效的 SHA-256 校验值")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, MaxBinarySize+1)); err != nil {
		return err
	}
	if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), expected) {
		return fmt.Errorf("文件 SHA-256 校验不匹配")
	}
	return nil
}

func (a *App) runtimeCore(k KernelState) (string, error) {
	if err := a.ensureStorage(); err != nil {
		return "", err
	}
	if !validID(k.File) {
		return "", fmt.Errorf("请先安装 mihomo 内核")
	}
	source := filepath.Join(a.root, "cores", k.File)
	if err := verifyStoredCore(source, k); err != nil {
		return "", err
	}
	name := "mihomo-" + k.SHA256[:16]
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dest := filepath.Join(a.run, name)
	if verifyFile(dest, k.SHA256) == nil {
		return dest, nil
	}
	if k.Compression == "xz" {
		if err := unpackCore(source, dest, k); err != nil {
			return "", err
		}
		return dest, nil
	}
	if err := copyVerifiedCore(source, dest, k.SHA256); err != nil {
		return "", err
	}
	return dest, nil
}

func (a *App) validateConfig(ctx context.Context, config []byte, k KernelState) error {
	return a.validateConfigIn(ctx, config, k, a.coreDataDir(), filepath.Join(a.root, "assets"))
}

func (a *App) coreDataDir() string {
	if a.storageMode == StorageUSB {
		return filepath.Join(a.root, "data")
	}
	return filepath.Join(a.run, "data")
}

func (a *App) validateConfigIn(ctx context.Context, config []byte, k KernelState, dataDir, assetsDir string) error {
	// Serialize temporary cores without holding the service lock. Waiting
	// imports remain cancellable and cannot multiply router memory usage.
	a.mu.Lock()
	if a.validationSlot == nil {
		a.validationSlot = make(chan struct{}, 1)
	}
	slot := a.validationSlot
	a.mu.Unlock()
	select {
	case slot <- struct{}{}:
		defer func() { <-slot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.dev && k.File == "" {
		return nil
	}
	binary, err := a.runtimeCore(k)
	if err != nil {
		return err
	}
	path := filepath.Join(a.run, "validate-"+randomID(8)+".yaml")
	if err = atomicWrite(path, config, 0600); err != nil {
		return err
	}
	defer os.Remove(path)
	validationCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err = os.MkdirAll(dataDir, 0700); err != nil {
		return err
	}
	cmd := exec.CommandContext(validationCtx, binary, "-t", "-d", dataDir, "-f", path)
	cmd.Env = coreEnvironment(assetsDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if len(output) > 6000 {
			output = output[len(output)-6000:]
		}
		return fmt.Errorf("mihomo 配置检查失败：%s", redact(string(output)))
	}
	return nil
}

func coreEnvironment(assetsDir string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(key, "SAFE_PATHS") && !strings.EqualFold(key, "SKIP_SAFE_PATH_CHECK") {
			env = append(env, entry)
		}
	}
	// File providers are confined to assets; keep mihomo's path checks enabled.
	return append(env, "SAFE_PATHS="+assetsDir, "SKIP_SAFE_PATH_CHECK=false")
}

func (a *App) startService(persistEnabled bool) error {
	return a.startPreparedService(persistEnabled, nil)
}

// Caller holds op; prepared bytes were validated against the current settings/kernel.
func (a *App) startPreparedService(persistEnabled bool, prepared []byte) error {
	if err := a.ensureStorage(); err != nil {
		return err
	}
	if a.isRunning() {
		if persistEnabled {
			s := a.snapshot()
			s.Enabled = true
			return a.persist(s)
		}
		return nil
	}
	s := a.snapshot()
	if !a.dev {
		if runtime.GOOS != "linux" || runtime.GOARCH != "arm64" {
			return fmt.Errorf("此安装包仅支持 Linux ARM64 路由器")
		}
		if v := strings.TrimSpace(commandOutput("dbus", "get", "ss_basic_enable")); v == "1" {
			return fmt.Errorf("fancyss 尚未关闭，请先关闭其科学上网开关，再启用长城 Clash")
		}
		if v := strings.TrimSpace(commandOutput("dbus", "get", "merlinclash_enable")); v == "1" {
			return fmt.Errorf("其他透明代理插件仍处于启用状态，请先关闭")
		}
		if err := a.preflightNetwork(s); err != nil {
			return err
		}
	}
	b := prepared
	var err error
	if b == nil {
		path, prepareErr := a.prepareConfig(s)
		if prepareErr != nil {
			return prepareErr
		}
		b, err = os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = a.validateConfig(a.operationContext(), b, s.Kernel); err != nil {
			return err
		}
	}
	active := filepath.Join(a.run, "active.yaml")
	if err = atomicWrite(active, b, 0600); err != nil {
		return err
	}
	if err = a.launchCore(s, active); err != nil {
		return err
	}
	dns, err := a.startDNS(s.Settings.DNSPort)
	if err != nil {
		_ = a.stopCore()
		return err
	}
	a.dns = dns
	if err = a.applyNetwork(s); err != nil {
		dns.Close()
		a.dns = nil
		_ = a.cleanupNetwork()
		_ = a.stopCore()
		return err
	}
	if persistEnabled {
		s.Enabled = true
		if err = a.persist(s); err != nil {
			_ = a.stopService(false)
			return err
		}
	}
	a.mu.Lock()
	a.lastError = ""
	a.mu.Unlock()
	a.log("info", "代理已启动："+s.Settings.Mode+" / "+s.Settings.DNSMode)
	return nil
}

func (a *App) launchCore(s State, path string) error {
	binary, err := a.runtimeCore(s.Kernel)
	if err != nil {
		return err
	}
	cmd := exec.Command(binary, "-d", a.coreDataDir(), "-f", path)
	cmd.Env = coreEnvironment(filepath.Join(a.root, "assets"))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("无法启动 mihomo：%w", err)
	}
	if err = atomicWrite(filepath.Join(a.run, "core.pid"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	core := &Core{cmd: cmd, done: make(chan struct{}), running: true}
	a.mu.Lock()
	a.core = core
	a.mu.Unlock()
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 64<<10)
		for scanner.Scan() {
			a.log("core", scanner.Text())
		}
	}()
	go func() {
		err := cmd.Wait()
		core.mu.Lock()
		core.running = false
		core.err = err
		core.mu.Unlock()
		close(core.done)
	}()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if !core.Running() {
			return fmt.Errorf("mihomo 启动后退出，请查看日志")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, err = a.controllerAt(ctx, s.Settings.ControllerPort, "GET", "/version", nil, nil)
		cancel()
		if err == nil {
			a.restoreSelections(s)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = a.stopCore()
	return fmt.Errorf("mihomo 控制接口未在 12 秒内就绪")
}

func (a *App) stopCore() error {
	a.mu.Lock()
	core := a.core
	a.core = nil
	a.mu.Unlock()
	_ = os.Remove(filepath.Join(a.run, "core.pid"))
	if core == nil || !core.Running() {
		return nil
	}
	if runtime.GOOS == "windows" {
		_ = core.cmd.Process.Kill()
	} else {
		_ = terminate(core.cmd.Process)
	}
	select {
	case <-core.done:
		return nil
	case <-time.After(4 * time.Second):
		_ = core.cmd.Process.Kill()
		<-core.done
		return nil
	}
}

func (a *App) stopService(persistDisabled bool) error {
	err := a.cleanupNetwork()
	if a.dns != nil {
		a.dns.Close()
		a.dns = nil
	}
	_ = a.stopCore()
	if persistDisabled {
		s := a.snapshot()
		s.Enabled = false
		if saveErr := a.persist(s); saveErr != nil {
			return saveErr
		}
	}
	if err == nil {
		a.log("info", "代理接管已撤销，恢复固件原有直连与 DNS")
	}
	return err
}

func (a *App) applyState(next State) error {
	previous := a.snapshot()
	if next.ActiveProfile == "" {
		return a.persist(next)
	}
	path, err := a.prepareConfig(next)
	if err != nil {
		return err
	}
	compiled, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err = a.validateConfig(a.operationContext(), compiled, next.Kernel); err != nil {
		return err
	}
	if !a.isRunning() {
		return a.persist(next)
	}
	// Stop reception first; no client traffic is sent into a half-reloaded core.
	if err = a.stopService(false); err != nil {
		return err
	}
	if err = a.persist(next); err != nil {
		_ = a.startService(false)
		return err
	}
	if err = a.startService(false); err != nil {
		if restoreErr := a.persist(previous); restoreErr != nil {
			_ = a.stopService(false)
			a.setFailure("配置应用失败，上一状态无法写回，代理保持关闭：" + restoreErr.Error())
			return fmt.Errorf("新配置启动失败：%w；上一状态恢复失败：%s", err, restoreErr)
		}
		restoreErr := a.startService(false)
		if restoreErr != nil {
			a.setFailure("配置应用失败，旧配置重启失败；已恢复直连：" + restoreErr.Error())
		}
		return fmt.Errorf("新配置应用失败，已尝试恢复上一配置：%w", err)
	}
	return nil
}

func (a *App) setMode(ctx context.Context, mode string) error {
	previous := a.snapshot()
	next := previous
	next.Settings.Mode = mode
	if !a.isRunning() {
		return a.persist(next)
	}
	if !a.dev {
		if err := a.preflightNetwork(next); err != nil {
			return err
		}
	}
	if _, err := a.controller(ctx, "PATCH", "/configs", nil, map[string]string{"mode": mode}); err != nil {
		return err
	}
	restore := func() {
		_, _ = a.controller(context.Background(), "PATCH", "/configs", nil, map[string]string{"mode": previous.Settings.Mode})
		if err := a.applyNetwork(previous); err != nil {
			_ = a.cleanupNetwork()
			a.setFailure("模式恢复未完成，已撤销网络接管：" + err.Error())
		}
	}
	if err := a.applyNetwork(next); err != nil {
		restore()
		return err
	}
	if err := a.persist(next); err != nil {
		restore()
		return err
	}
	return nil
}

func (a *App) controller(ctx context.Context, method, path string, query map[string]string, body any) (any, error) {
	if !a.isRunning() {
		return nil, fmt.Errorf("mihomo 未运行")
	}
	return a.controllerAt(ctx, a.snapshot().Settings.ControllerPort, method, path, query, body)
}

func (a *App) controllerAt(ctx context.Context, port int, method, path string, query map[string]string, body any) (any, error) {
	address := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		address += "?" + q.Encode()
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, method, address, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.secret)
	req.Header.Set("Content-Type", "application/json")
	res, err := controllerHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("内核接口连接失败")
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 12<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("内核返回 HTTP %d：%s", res.StatusCode, redact(string(b)))
	}
	if len(b) == 0 {
		return map[string]any{}, nil
	}
	var result any
	if err = json.Unmarshal(b, &result); err != nil {
		return nil, fmt.Errorf("内核接口返回无效 JSON")
	}
	return result, nil
}

func escapeSegment(s string) string { return url.PathEscape(s) }

func (a *App) selectGroup(ctx context.Context, group, node string) error {
	a.op.Lock()
	defer a.op.Unlock()
	s := a.snapshot()
	if !validID(s.ActiveProfile) || group == "" || node == "" || len(group) > 512 || len(node) > 512 || strings.ContainsAny(group+node, "\x00\r\n") {
		return fmt.Errorf("代理组或节点名称无效")
	}
	path := "/proxies/" + escapeSegment(group)
	before, err := a.controller(ctx, "GET", path, nil, nil)
	if err != nil {
		return err
	}
	oldNode := ""
	if m, ok := before.(map[string]any); ok {
		oldNode, _ = m["now"].(string)
	}
	if _, err = a.controller(ctx, "PUT", path, nil, map[string]string{"name": node}); err != nil {
		return err
	}
	if s.Selections == nil {
		s.Selections = map[string]map[string]string{}
	}
	if s.Selections[s.ActiveProfile] == nil {
		s.Selections[s.ActiveProfile] = map[string]string{}
	}
	s.Selections[s.ActiveProfile][group] = node
	if err = a.persist(s); err != nil {
		if oldNode != "" {
			_, _ = a.controller(context.Background(), "PUT", path, nil, map[string]string{"name": oldNode})
		}
		return fmt.Errorf("选择记录保存失败，已尝试恢复原线路：%w", err)
	}
	return nil
}

func (a *App) restoreSelections(s State) {
	ctx, cancel := context.WithTimeout(a.operationContext(), 3*time.Second)
	defer cancel()
	for group, node := range s.Selections[s.ActiveProfile] {
		if _, err := a.controllerAt(ctx, s.Settings.ControllerPort, "PUT", "/proxies/"+escapeSegment(group), nil, map[string]string{"name": node}); err != nil {
			a.log("warning", "未能恢复代理组选择 "+group+"，暂用配置默认选择")
		}
	}
}
func (a *App) operationContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) setFailure(message string) {
	a.mu.Lock()
	a.lastError = redact(message)
	a.mu.Unlock()
	a.log("error", message)
}

func (a *App) background() {
	go a.scheduledUpdates()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var checkedCore *Core
	var nextCheck time.Time
	healthFailures := 0
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			if !a.op.TryLock() {
				continue
			}
			a.mu.RLock()
			core := a.core
			a.mu.RUnlock()
			if checkedCore != core {
				checkedCore = core
				healthFailures = 0
				nextCheck = time.Time{}
			}
			if core != nil {
				if !core.Running() {
					a.failOpen("mihomo 意外退出")
				} else if err := a.ensureStorage(); err != nil {
					a.failOpen("U 盘挂载不可用：" + err.Error())
				} else if _, err := os.Stat(filepath.Join(a.root, "state.json")); err != nil {
					a.failOpen("插件存储不可用")
				} else if !time.Now().Before(nextCheck) {
					ctx, cancel := context.WithTimeout(a.ctx, 2*time.Second)
					_, err := a.controllerAt(ctx, a.snapshot().Settings.ControllerPort, "GET", "/version", nil, nil)
					cancel()
					nextCheck = time.Now().Add(3 * time.Second)
					if err == nil {
						healthFailures = 0
					} else {
						healthFailures++
						if healthFailures >= 5 {
							a.failOpen("mihomo 控制接口连续无响应")
						}
					}
				}
			}
			a.op.Unlock()
		}
	}
}

func (a *App) failOpen(reason string) {
	a.cancelProfileTasks()
	if err := a.stopService(false); err != nil {
		a.setFailure(reason + "；网络规则清理未完全成功，请使用紧急恢复：" + err.Error())
		return
	}
	a.setFailure(reason + "，代理和 DNS 接管已清理，当前使用直连")
}

func (a *App) scheduledUpdates() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			for _, profile := range a.snapshot().Profiles {
				if profile.URL == "" || profile.UpdateHours <= 0 {
					continue
				}
				if time.Now().Before(nextProfileUpdate(profile)) {
					continue
				}
				err := a.updateProfile(a.ctx, profile.ID)
				if err != nil {
					a.log("warning", "订阅自动更新失败，保留旧配置："+err.Error())
				}
			}
		}
	}
}
