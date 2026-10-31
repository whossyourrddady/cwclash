// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) dispatch(ctx context.Context, action string, p json.RawMessage) (any, error) {
	if !contains([]string{"status", "state.get", "jobs.get", "logs", "probe", "storage.inspect", "service.stop", "connectivity.get", "connectivity.check", "connectivity.media"}, action) {
		if err := a.ensureStorage(); err != nil {
			return nil, err
		}
	}
	var q struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		URL         string `json:"url"`
		UserAgent   string `json:"userAgent"`
		UpdateHours int    `json:"updateHours"`
		Content     string `json:"content"`
		Revision    string `json:"revision"`
		Group       string `json:"group"`
		Node        string `json:"node"`
		Timeout     int    `json:"timeout"`
		Since       uint64 `json:"since"`
		Mode        string `json:"mode"`
		Type        string `json:"type"`
		Enabled     bool   `json:"enabled"`
		SHA256      string `json:"sha256"`
		Bytes       int64  `json:"bytes"`
	}
	if err := json.Unmarshal(p, &q); err != nil {
		return nil, fmt.Errorf("参数格式无效")
	}
	switch action {
	case "connectivity.get":
		return a.connectivityState(), nil
	case "connectivity.check":
		return a.beginConnectivity("basic")
	case "connectivity.media":
		return a.beginConnectivity("media")
	case "status":
		return a.status(ctx), nil
	case "state.get":
		s := a.snapshot()
		updates := map[string]string{}
		for _, p := range s.Profiles {
			if next := nextProfileUpdate(p); !next.IsZero() {
				updates[p.ID] = next.Format(time.RFC3339)
			}
		}
		return map[string]any{"state": s, "version": a.version, "dev": a.dev, "nextUpdates": updates}, nil
	case "probe":
		return Probe(), nil
	case "storage.inspect", "storage.cleanAsd", "storage.undoAsd":
		return a.newJob(action, func() (any, error) {
			a.op.Lock()
			defer a.op.Unlock()
			switch action {
			case "storage.cleanAsd":
				if err := a.cleanASD(ASDCacheReport{Bytes: q.Bytes, SHA256: q.SHA256}); err != nil {
					return nil, err
				}
			case "storage.undoAsd":
				a.recoverASDBackup()
				if a.dev || a.asdBackup == nil {
					return nil, fmt.Errorf("没有可还原的内存检查备份")
				}
				if err := undoASDFile(asdCachePath, *a.asdBackup); err != nil {
					return nil, err
				}
				_ = os.Remove(a.asdBackup.Path)
				a.asdBackup = nil
				_ = os.Remove(filepath.Join(a.run, "asd-cleanup.json"))
			}
			return a.inspectStorage()
		}), nil
	case "jobs.get":
		a.mu.RLock()
		defer a.mu.RUnlock()
		j := a.jobs[q.ID]
		if j == nil {
			return nil, fmt.Errorf("任务不存在或已过期")
		}
		copy := *j
		return copy, nil
	case "service.start", "service.preview", "service.stop", "service.restart", "service.nat":
		if action == "service.stop" {
			a.cancelProfileTasks()
		}
		return a.newJob(action, func() (any, error) {
			a.op.Lock()
			defer a.op.Unlock()
			switch action {
			case "service.preview":
				return nil, a.startPreview()
			case "service.stop":
				return nil, a.stopService(true)
			case "service.nat":
				if a.isPreview() {
					return nil, nil
				}
				if a.isRunning() {
					return nil, a.applyNetwork(a.snapshot())
				}
				if a.snapshot().Enabled {
					return nil, a.startService(false)
				}
				return nil, a.cleanupNetwork()
			case "service.restart":
				if err := a.stopService(false); err != nil {
					return nil, err
				}
				if a.isPreview() {
					return nil, a.startService(false)
				}
			}
			return nil, a.startService(true)
		}), nil
	case "settings.save":
		var settings Settings
		if err := json.Unmarshal(p, &settings); err != nil {
			return nil, err
		}
		if err := validateSettings(settings); err != nil {
			return nil, err
		}
		return a.newJob(action, func() (any, error) {
			a.op.Lock()
			defer a.op.Unlock()
			s := a.snapshot()
			s.Settings = settings
			return nil, a.applyState(s)
		}), nil
	case "devices.list":
		return connectedDevices(a.snapshot().Devices), nil
	case "devices.save":
		var data struct {
			Devices []DevicePolicy `json:"devices"`
		}
		if err := json.Unmarshal(p, &data); err != nil {
			return nil, err
		}
		if len(data.Devices) > 256 {
			return nil, fmt.Errorf("设备规则过多")
		}
		seen := map[string]bool{}
		for i := range data.Devices {
			d := &data.Devices[i]
			if d.ID == "" {
				d.ID = randomID(8)
			}
			if !validID(d.ID) || seen[d.ID] {
				return nil, fmt.Errorf("设备标识无效或重复")
			}
			seen[d.ID] = true
			if net.ParseIP(d.IP) == nil {
				return nil, fmt.Errorf("请为设备填写有效的 IP 地址")
			}
			if !contains([]string{"direct", "rule", "proxy"}, d.Mode) {
				return nil, fmt.Errorf("设备模式无效")
			}
			if d.MAC != "" {
				if _, err := net.ParseMAC(d.MAC); err != nil {
					return nil, fmt.Errorf("MAC 地址无效")
				}
			}
		}
		return a.newJob(action, func() (any, error) {
			a.op.Lock()
			defer a.op.Unlock()
			s := a.snapshot()
			s.Devices = data.Devices
			return nil, a.applyState(s)
		}), nil
	case "profiles.save":
		return a.newJob(action, func() (any, error) {
			return a.saveProfile(a.operationContext(), Profile{ID: q.ID, Name: q.Name, URL: q.URL, UserAgent: q.UserAgent, UpdateHours: q.UpdateHours}, q.Content, q.Revision)
		}), nil
	case "profiles.read":
		a.op.Lock()
		defer a.op.Unlock()
		if !validID(q.ID) {
			return nil, fmt.Errorf("配置标识无效")
		}
		b, err := os.ReadFile(filepath.Join(a.root, "profiles", q.ID+".yaml"))
		if err != nil {
			return nil, err
		}
		profile := findProfile(a.snapshot(), q.ID)
		if profile == nil {
			return nil, fmt.Errorf("配置不存在")
		}
		return map[string]any{"content": string(b), "profile": profile, "revision": profileRevision(profile, b)}, nil
	case "profiles.select":
		return a.newJob(action, func() (any, error) {
			a.op.Lock()
			defer a.op.Unlock()
			s := a.snapshot()
			if findProfile(s, q.ID) == nil {
				return nil, fmt.Errorf("配置不存在")
			}
			s.ActiveProfile = q.ID
			return nil, a.applyState(s)
		}), nil
	case "profiles.update":
		return a.newJob(action, func() (any, error) {
			return nil, a.updateProfile(a.operationContext(), q.ID)
		}), nil
	case "profiles.delete":
		a.op.Lock()
		defer a.op.Unlock()
		s := a.snapshot()
		if s.ActiveProfile == q.ID {
			if a.isRunning() {
				return nil, fmt.Errorf("请先停止代理，再删除当前配置")
			}
			if err := a.stopService(false); err != nil {
				return nil, err
			}
			s.ActiveProfile = ""
			s.Enabled = false
		}
		var out []Profile
		for _, v := range s.Profiles {
			if v.ID != q.ID {
				out = append(out, v)
			}
		}
		s.Profiles = out
		delete(s.Selections, q.ID)
		if err := a.persist(s); err != nil {
			return nil, err
		}
		if validID(q.ID) {
			_ = os.Remove(filepath.Join(a.root, "profiles", q.ID+".yaml"))
		}
		return nil, nil
	case "core.check":
		return a.newJob(action, func() (any, error) { return a.checkRelease(a.operationContext()) }), nil
	case "core.upgrade":
		return a.newJob(action, func() (any, error) { a.op.Lock(); defer a.op.Unlock(); return nil, a.upgradeCore(a.operationContext()) }), nil
	case "core.rollback":
		return a.newJob(action, func() (any, error) { a.op.Lock(); defer a.op.Unlock(); return nil, a.rollbackCore() }), nil
	case "upload.begin", "upload.chunk", "upload.finish", "upload.cancel":
		return a.handleUpload(ctx, action, p)
	case "download.begin", "download.chunk", "download.cancel":
		return a.handleDownload(action, p)
	case "logs":
		a.mu.RLock()
		defer a.mu.RUnlock()
		out := []LogEntry{}
		for _, entry := range a.logs {
			if entry.ID > q.Since {
				out = append(out, entry)
			}
		}
		return out, nil
	case "dns.history":
		a.mu.RLock()
		defer a.mu.RUnlock()
		out := []DNSQuery{}
		for _, entry := range a.dnsQueries {
			if entry.ID > q.Since {
				out = append(out, entry)
			}
		}
		return out, nil
	case "dns.query":
		name := strings.TrimSpace(q.Name)
		if name == "" || strings.ContainsAny(name, "\r\n\x00 /\\") || len(name) > 253 {
			return nil, fmt.Errorf("域名无效")
		}
		typ := q.Type
		if typ == "" {
			typ = "A"
		}
		if !contains([]string{"A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA", "HTTPS", "SVCB"}, typ) {
			return nil, fmt.Errorf("DNS 查询类型无效")
		}
		return a.controller(ctx, "GET", "/dns/query", map[string]string{"name": name, "type": typ}, nil)
	case "groups":
		if !a.isRunning() {
			return a.previewGroups()
		}
		return a.controller(ctx, "GET", "/proxies", nil, nil)
	case "groups.select":
		if q.Group == "" || q.Node == "" {
			return nil, fmt.Errorf("请选择代理组和节点")
		}
		return nil, a.selectGroup(ctx, q.Group, q.Node)
	case "groups.delay":
		if q.Group == "" {
			return nil, fmt.Errorf("请选择节点")
		}
		timeout := q.Timeout
		if timeout < 1000 || timeout > 15000 {
			timeout = 5000
		}
		target := q.URL
		if target == "" {
			target = "https://www.gstatic.com/generate_204"
		}
		if err := validHTTPURL(target); err != nil {
			return nil, err
		}
		return a.controller(ctx, "GET", "/proxies/"+escapeSegment(q.Group)+"/delay", map[string]string{"url": target, "timeout": fmt.Sprint(timeout)}, nil)
	case "connections":
		return a.controller(ctx, "GET", "/connections", nil, nil)
	case "connections.close":
		path := "/connections"
		if q.ID != "" {
			path += "/" + escapeSegment(q.ID)
		}
		return a.controller(ctx, "DELETE", path, nil, nil)
	case "rules":
		return a.controller(ctx, "GET", "/rules", nil, nil)
	case "providers":
		return a.providers(ctx)
	case "providers.update":
		if q.ID == "" || !contains([]string{"proxies", "rules"}, q.Type) {
			return nil, fmt.Errorf("规则/代理集合参数无效")
		}
		return a.controller(ctx, "PUT", "/providers/"+q.Type+"/"+escapeSegment(q.ID), nil, nil)
	case "mode.set":
		if !contains([]string{"rule", "global", "direct"}, q.Mode) {
			return nil, fmt.Errorf("代理模式无效")
		}
		a.op.Lock()
		defer a.op.Unlock()
		return nil, a.setMode(ctx, q.Mode)
	case "cache.clear":
		var errs []string
		for _, path := range []string{"/cache/dns/flush", "/cache/fakeip/flush"} {
			if _, err := a.controller(ctx, "POST", path, nil, nil); err != nil {
				errs = append(errs, err.Error())
			}
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("%s", strings.Join(errs, "；"))
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("不支持的操作：%s", action)
	}
}

func (a *App) status(ctx context.Context) map[string]any {
	s := a.snapshot()
	a.mu.RLock()
	core := a.core
	running := core != nil && core.Running()
	lastError := a.lastError
	started := a.started
	a.mu.RUnlock()
	data := map[string]any{"running": running, "enabled": s.Enabled, "version": a.version, "kernel": s.Kernel, "activeProfile": s.ActiveProfile, "mode": s.Settings.Mode, "uptime": int(time.Since(started).Seconds()), "error": lastError, "dev": a.dev, "network": a.networkSummary(s), "storageMode": a.storageMode}
	data["preview"] = running && a.isPreview()
	if running {
		if c, err := a.controller(ctx, "GET", "/connections", nil, nil); err == nil {
			if m, ok := c.(map[string]any); ok {
				data["uploadTotal"] = m["uploadTotal"]
				data["downloadTotal"] = m["downloadTotal"]
				if connections, ok := m["connections"].([]any); ok {
					data["connectionCount"] = len(connections)
				}
			}
		}
		data["memory"] = core.Memory()
	}
	return data
}

func (a *App) providers(ctx context.Context) (any, error) {
	proxies, err := a.controller(ctx, "GET", "/providers/proxies", nil, nil)
	if err != nil {
		return nil, err
	}
	rules, err := a.controller(ctx, "GET", "/providers/rules", nil, nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"proxies": proxies, "rules": rules}, nil
}

func findProfile(s State, id string) *Profile {
	for i := range s.Profiles {
		if s.Profiles[i].ID == id {
			return &s.Profiles[i]
		}
	}
	return nil
}
