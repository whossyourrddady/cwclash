// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

func parseYAML(data []byte) (map[string]any, error) {
	if len(data) == 0 || len(data) > MaxConfigSize {
		return nil, fmt.Errorf("配置为空或超过 8 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("YAML 解析失败：%w", err)
	}
	if doc == nil {
		return nil, fmt.Errorf("配置根节点必须是映射")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("每个配置文件只允许一个 YAML 文档")
	}
	if _, nodes := doc["proxies"]; !nodes {
		if _, providers := doc["proxy-providers"]; !providers {
			if _, rules := doc["rules"]; !rules {
				return nil, fmt.Errorf("文件不是 Clash/mihomo 配置：缺少 proxies、proxy-providers 或 rules")
			}
		}
	}
	return doc, nil
}

func mergeMap(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				mergeMap(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func asList(v any) []any {
	if a, ok := v.([]any); ok {
		return a
	}
	return nil
}

func (a *App) compileConfig(s State, raw []byte, controllerPort int) ([]byte, error) {
	doc, err := parseYAML(raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(s.Settings.OverrideYAML) != "" {
		var override map[string]any
		if err = yaml.Unmarshal([]byte(s.Settings.OverrideYAML), &override); err != nil {
			return nil, fmt.Errorf("覆写 YAML 无效：%w", err)
		}
		mergeMap(doc, override)
	}
	group := s.Settings.DefaultGroup
	available := map[string]bool{"DIRECT": true, "REJECT": true, "GLOBAL": true}
	for _, list := range []string{"proxy-groups", "proxies"} {
		for _, v := range asList(doc[list]) {
			m := asMap(v)
			if name, ok := m["name"].(string); ok && name != "" {
				available[name] = true
				if group == "" {
					group = name
				}
			}
		}
	}
	if group == "" {
		group = "DIRECT"
	}
	if !available[group] {
		return nil, fmt.Errorf("默认策略组 %q 不在当前配置中", group)
	}
	prepend := []any{}
	for _, device := range s.Devices {
		if device.Mode == "rule" {
			continue
		}
		ip := net.ParseIP(device.IP)
		if ip == nil {
			return nil, fmt.Errorf("设备 IP 无效")
		}
		target := "DIRECT"
		if device.Mode == "proxy" {
			target = device.Group
			if target == "" {
				target = group
			}
			if !available[target] {
				return nil, fmt.Errorf("设备 %s 的策略组 %q 不存在", device.Name, target)
			}
		}
		prefix := "SRC-IP-CIDR,"
		bits := "/32"
		if ip.To4() == nil {
			bits = "/128"
		}
		prepend = append(prepend, prefix+ip.String()+bits+","+target)
	}
	for _, d := range s.Settings.DirectDomains {
		prepend = append(prepend, "DOMAIN-SUFFIX,"+strings.TrimPrefix(d, "+.")+",DIRECT")
	}
	for _, cidr := range s.Settings.DirectCIDRs {
		kind := "IP-CIDR"
		if strings.Contains(cidr, ":") {
			kind = "IP-CIDR6"
		}
		prepend = append(prepend, kind+","+cidr+",DIRECT,no-resolve")
	}
	for _, d := range s.Settings.ProxyDomains {
		prepend = append(prepend, "DOMAIN-SUFFIX,"+strings.TrimPrefix(d, "+.")+","+group)
	}
	for _, cidr := range s.Settings.ProxyCIDRs {
		kind := "IP-CIDR"
		if strings.Contains(cidr, ":") {
			kind = "IP-CIDR6"
		}
		prepend = append(prepend, kind+","+cidr+","+group+",no-resolve")
	}
	for _, rule := range s.Settings.CustomRules {
		rule = strings.TrimSpace(rule)
		if rule != "" {
			if strings.ContainsAny(rule, "\r\n\x00") {
				return nil, fmt.Errorf("自定义规则包含无效换行")
			}
			prepend = append(prepend, rule)
		}
	}
	rules := asList(doc["rules"])
	if len(rules) == 0 {
		rules = []any{"MATCH," + group}
	}
	doc["rules"] = append(prepend, rules...)
	// Router-owned listeners and controller settings cannot be exposed by a subscription.
	for _, key := range []string{"port", "socks-port"} {
		doc[key] = 0
	}
	doc["mixed-port"] = s.Settings.MixedPort
	doc["redir-port"] = s.Settings.RedirPort
	doc["tproxy-port"] = s.Settings.TProxyPort
	if a.dev {
		doc["redir-port"] = 0
		doc["tproxy-port"] = 0
	}
	doc["allow-lan"] = true
	doc["bind-address"] = "*"
	if a.dev {
		doc["allow-lan"] = false
		doc["bind-address"] = "127.0.0.1"
	}
	doc["listeners"] = []any{}
	doc["tun"] = map[string]any{"enable": false}
	doc["external-controller"] = fmt.Sprintf("127.0.0.1:%d", controllerPort)
	doc["secret"] = a.secret
	for _, key := range []string{"external-controller-tls", "external-controller-unix", "external-controller-pipe", "external-controller-cors", "external-ui", "external-ui-url", "external-doh-server", "external-controller-routing-mark"} {
		delete(doc, key)
	}
	doc["routing-mark"] = OutboundMark
	if a.dev {
		doc["routing-mark"] = 0
	}
	doc["find-process-mode"] = "off"
	doc["mode"] = s.Settings.Mode
	doc["log-level"] = s.Settings.LogLevel
	doc["geodata-loader"] = "memconservative"
	profile := asMap(doc["profile"])
	profile["store-selected"] = false // Manual choices are stored per profile in state.json.
	profile["store-fake-ip"] = true
	doc["profile"] = profile
	ipv6 := s.Settings.IPv6 != "block" && a.ipv6Available()
	doc["ipv6"] = ipv6
	dns := asMap(doc["dns"])
	dns["enable"] = true
	dns["listen"] = "127.0.0.1:1053"
	dns["enhanced-mode"] = s.Settings.DNSMode
	dns["ipv6"] = ipv6
	if _, ok := dns["default-nameserver"]; !ok {
		dns["default-nameserver"] = []string{"223.5.5.5", "119.29.29.29"}
	}
	if _, ok := dns["nameserver"]; !ok {
		dns["nameserver"] = []string{"https://dns.alidns.com/dns-query", "https://doh.pub/dns-query"}
	}
	if _, ok := dns["proxy-server-nameserver"]; !ok {
		dns["proxy-server-nameserver"] = []string{"223.5.5.5", "119.29.29.29"}
	}
	if s.Settings.DNSMode == "fake-ip" {
		dns["fake-ip-range"] = "198.18.0.1/16"
		if ipv6 {
			dns["fake-ip-range6"] = "fc00:198:18::1/64"
		}
	}
	doc["dns"] = dns
	if err = a.normalizeProviderPaths(doc); err != nil {
		return nil, err
	}
	return yaml.Marshal(doc)
}

func (a *App) normalizeProviderPaths(doc map[string]any) error {
	for _, kind := range []string{"proxy-providers", "rule-providers"} {
		providers := asMap(doc[kind])
		for name, value := range providers {
			p := asMap(value)
			if typ, _ := p["type"].(string); typ == "file" {
				path, _ := p["path"].(string)
				clean, err := safeRelative(path)
				if err != nil {
					return fmt.Errorf("集合 %s：文件路径必须位于 assets 目录内", name)
				}
				p["path"] = filepath.Join(a.root, "assets", clean)
			} else {
				// Include source identity so equal names from different subscriptions cannot share cache.
				identity := make(map[string]any, len(p))
				for k, v := range p {
					if k != "path" {
						identity[k] = v
					}
				}
				encoded, err := json.Marshal(identity)
				if err != nil {
					return fmt.Errorf("集合配置无法序列化")
				}
				hash := sha256.Sum256(append([]byte(kind+":"+name+":"), encoded...))
				p["path"] = filepath.Join(a.coreDataDir(), "providers", hex.EncodeToString(hash[:12])+".yaml")
			}
			providers[name] = p
		}
		if len(providers) > 0 {
			doc[kind] = providers
		}
	}
	return nil
}

func safeRelative(path string) (string, error) {
	path = strings.ReplaceAll(path, "\\", "/")
	if path == "" || strings.ContainsAny(path, "\x00:") || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("路径无效")
	}
	for _, p := range strings.Split(path, "/") {
		if p == ".." {
			return "", fmt.Errorf("路径越界")
		}
	}
	clean := filepath.FromSlash(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./"))
	if clean == "." || clean == "" {
		return "", fmt.Errorf("路径必须指向文件")
	}
	return clean, nil
}

func validHTTPURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || !(u.Scheme == "https" || u.Scheme == "http") || u.Fragment != "" {
		return fmt.Errorf("请输入有效的 HTTP/HTTPS 地址")
	}
	return nil
}

func (a *App) fetch(ctx context.Context, address, userAgent string, max int64) ([]byte, http.Header, error) {
	if err := validHTTPURL(address); err != nil {
		return nil, nil, err
	}
	if len(userAgent) > 256 || strings.ContainsAny(userAgent, "\r\n") {
		return nil, nil, fmt.Errorf("User-Agent 无效")
	}
	if userAgent == "" {
		userAgent = "clash.meta / cwclash"
	}
	proxies := []func(*http.Request) (*url.URL, error){nil}
	if a.isRunning() {
		proxy, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", a.snapshot().Settings.MixedPort))
		proxies = append(proxies, http.ProxyURL(proxy))
	}
	var last error
	for _, proxy := range proxies {
		requestCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
		req, err := http.NewRequestWithContext(requestCtx, "GET", address, nil)
		if err != nil {
			cancel()
			return nil, nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		transport := &http.Transport{Proxy: proxy, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 20 * time.Second}
		client := &http.Client{Transport: transport, Timeout: 40 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return fmt.Errorf("重定向过多")
			}
			if err := validHTTPURL(req.URL.String()); err != nil {
				return err
			}
			if len(via) > 0 && via[0].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return fmt.Errorf("拒绝降级为 HTTP")
			}
			return nil
		}}
		res, err := client.Do(req)
		if err != nil {
			cancel()
			transport.CloseIdleConnections()
			last = fmt.Errorf("下载连接失败：%w", err)
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			cancel()
			transport.CloseIdleConnections()
			last = fmt.Errorf("下载返回 HTTP %d", res.StatusCode)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, max+1))
		res.Body.Close()
		cancel()
		transport.CloseIdleConnections()
		if err != nil {
			last = err
			continue
		}
		if int64(len(data)) > max {
			return nil, nil, fmt.Errorf("下载文件超过允许大小")
		}
		return data, res.Header, nil
	}
	return nil, nil, last
}

func (a *App) saveProfile(ctx context.Context, profile Profile, content string) (Profile, error) {
	if err := a.ensureStorage(); err != nil {
		return profile, err
	}
	s := a.snapshot()
	if profile.ID == "" {
		profile.ID = randomID(10)
	}
	if !validID(profile.ID) {
		return profile, fmt.Errorf("配置标识无效")
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" || len(profile.Name) > 100 {
		return profile, fmt.Errorf("配置名称需为 1–100 个字符")
	}
	if profile.UpdateHours < 0 || profile.UpdateHours > 720 {
		return profile, fmt.Errorf("更新间隔应为 0–720 小时，0 表示手动更新")
	}
	if profile.URL != "" {
		if err := validHTTPURL(profile.URL); err != nil {
			return profile, err
		}
	}
	if content == "" {
		if profile.URL == "" {
			return profile, fmt.Errorf("请提供订阅地址或配置内容")
		}
		data, headers, err := a.fetch(ctx, profile.URL, profile.UserAgent, MaxConfigSize)
		if err != nil {
			return profile, err
		}
		content = string(data)
		profile.SubscriptionInfo = headers.Get("Subscription-Userinfo")
	}
	compiled, err := a.compileConfig(s, []byte(content), s.Settings.ControllerPort)
	if err != nil {
		return profile, err
	}
	if err = a.validateConfig(ctx, compiled, s.Kernel); err != nil {
		return profile, err
	}
	path := filepath.Join(a.root, "profiles", profile.ID+".yaml")
	old, oldErr := os.ReadFile(path)
	if oldErr != nil && !os.IsNotExist(oldErr) {
		return profile, oldErr
	}
	previous := a.snapshot()
	restart := a.isRunning() && s.ActiveProfile == profile.ID && !bytes.Equal(old, []byte(content))
	if restart {
		if err = a.stopService(false); err != nil {
			return profile, err
		}
	}
	rollback := func(cause error) (Profile, error) {
		if restart {
			_ = a.stopService(false)
		}
		var restoreErr error
		if oldErr == nil {
			restoreErr = atomicWrite(path, old, 0600)
		} else {
			restoreErr = os.Remove(path)
			if os.IsNotExist(restoreErr) {
				restoreErr = nil
			}
		}
		if restoreErr == nil {
			restoreErr = a.persist(previous)
		}
		if restoreErr == nil && restart {
			restoreErr = a.startService(false)
		}
		if restoreErr != nil {
			a.setFailure("配置保存失败，自动回退未完全完成，请检查当前状态：" + restoreErr.Error())
			return profile, fmt.Errorf("配置保存失败：%w；自动回退未完成：%s", cause, restoreErr)
		}
		return profile, fmt.Errorf("配置保存失败，已恢复上一配置：%w", cause)
	}
	if err = atomicWrite(path, []byte(content), 0600); err != nil {
		return rollback(err)
	}
	profile.UpdatedAt = time.Now().Format(time.RFC3339)
	profile.LastAttempt = profile.UpdatedAt
	profile.UpdateError = ""
	profile.UpdateFailures = 0
	profile.RetryAt = ""
	found := false
	for i := range s.Profiles {
		if s.Profiles[i].ID == profile.ID {
			s.Profiles[i] = profile
			found = true
			break
		}
	}
	if !found {
		s.Profiles = append(s.Profiles, profile)
	}
	if s.ActiveProfile == "" {
		s.ActiveProfile = profile.ID
	}
	if err = a.persist(s); err != nil {
		return rollback(err)
	}
	if restart {
		if err = a.startService(false); err != nil {
			return rollback(err)
		}
	}
	a.log("info", "配置已保存："+profile.Name)
	return profile, nil
}

func (a *App) updateProfile(ctx context.Context, id string) error {
	if err := a.ensureStorage(); err != nil {
		return err
	}
	p := findProfile(a.snapshot(), id)
	if p == nil {
		return fmt.Errorf("配置不存在")
	}
	if p.URL == "" {
		return fmt.Errorf("本地配置没有订阅地址")
	}
	_, err := a.saveProfile(ctx, *p, "")
	if err != nil {
		next := a.snapshot()
		for i := range next.Profiles {
			if next.Profiles[i].ID != id {
				continue
			}
			entry := &next.Profiles[i]
			entry.LastAttempt = time.Now().Format(time.RFC3339)
			entry.UpdateError = redact(err.Error())
			if len(entry.UpdateError) > 2000 {
				entry.UpdateError = "订阅更新失败，详见日志"
			}
			if entry.UpdateFailures < 7 {
				entry.UpdateFailures++
			}
			entry.RetryAt = time.Now().Add(updateBackoff(entry.UpdateFailures)).Format(time.RFC3339)
		}
		if persistErr := a.persist(next); persistErr != nil {
			return fmt.Errorf("%w；无法保存重试状态：%v", err, persistErr)
		}
	}
	return err
}

func (a *App) prepareConfig(s State) (string, error) {
	if !validID(s.ActiveProfile) {
		return "", fmt.Errorf("请先导入并选择 Clash/mihomo 配置")
	}
	raw, err := os.ReadFile(filepath.Join(a.root, "profiles", s.ActiveProfile+".yaml"))
	if err != nil {
		return "", err
	}
	b, err := a.compileConfig(s, raw, s.Settings.ControllerPort)
	if err != nil {
		return "", err
	}
	path := filepath.Join(a.run, "candidate.yaml")
	if err = atomicWrite(path, b, 0600); err != nil {
		return "", err
	}
	return path, nil
}
