// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

type HealthTarget struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Group  string `json:"group"`
	URL    string `json:"-"`
	Parser string `json:"-"`
}
type HealthResult struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Group     string `json:"group"`
	State     string `json:"state"`
	Detail    string `json:"detail"`
	HTTP      int    `json:"http,omitempty"`
	LatencyMS int64  `json:"latencyMs,omitempty"`
	Region    string `json:"region,omitempty"`
	IP        string `json:"ip,omitempty"`
	Unlock    string `json:"unlock,omitempty"`
}
type InterfaceHealth struct {
	Name      string   `json:"name"`
	State     string   `json:"state"`
	Addresses []string `json:"addresses"`
	Carrier   string   `json:"carrier"`
	Detail    string   `json:"detail"`
}
type HealthReport struct {
	StartedAt  string           `json:"startedAt"`
	FinishedAt string           `json:"finishedAt"`
	Signature  string           `json:"signature"`
	Route      string           `json:"route"`
	LAN        *InterfaceHealth `json:"lan,omitempty"`
	WAN        *InterfaceHealth `json:"wan,omitempty"`
	Results    []HealthResult   `json:"results"`
}

var siteTargets = []HealthTarget{
	{"baidu", "百度", "direct", "https://www.baidu.com/", ""},
	{"aliyun", "阿里云", "direct", "https://www.aliyun.com/", ""},
	{"github", "GitHub", "sites", "https://github.com/", ""},
	{"google", "Google", "sites", "https://www.google.com/generate_204", ""},
	{"chatgpt", "ChatGPT", "ai", "https://chatgpt.com/", ""},
	{"claude", "Claude", "ai", "https://claude.ai/", ""},
	{"gemini", "Gemini", "ai", "https://gemini.google.com/", ""},
	{"youtube", "YouTube", "sites", "https://www.youtube.com/", ""},
	{"x", "X", "sites", "https://x.com/", ""},
	{"telegram", "Telegram", "sites", "https://telegram.org/", ""},
	{"discord", "Discord", "sites", "https://discord.com/", ""},
	{"reddit", "Reddit", "sites", "https://www.reddit.com/", ""},
	{"wikipedia", "Wikipedia", "sites", "https://www.wikipedia.org/", ""},
	{"bing", "Bing", "sites", "https://www.bing.com/", ""},
	{"exit", "检测出口", "exit", "https://www.cloudflare.com/cdn-cgi/trace", "trace"},
}
var mediaTargets = []HealthTarget{
	{"netflix", "Netflix", "media", "https://www.netflix.com/title/81280792", "netflix"},
	{"netflix-catalog", "Netflix 片库样本", "media", "https://www.netflix.com/title/70143836", "netflix"},
	{"youtube-premium", "YouTube Premium", "media", "https://www.youtube.com/premium", "youtube"},
	{"disney", "Disney+", "media", "https://www.disneyplus.com/", "disney"},
	{"spotify", "Spotify", "media", "https://www.spotify.com/us/signup/", "spotify"},
	{"prime", "Prime Video", "media", "https://www.primevideo.com/", "generic"},
	{"max", "Max / HBO Max", "media", "https://www.hbomax.com/", "generic"},
	{"appletv", "Apple TV", "media", "https://tv.apple.com/", "generic"},
	{"twitch", "Twitch", "media", "https://www.twitch.tv/", "generic"},
}

func (a *App) healthSignature() string {
	s := a.snapshot()
	a.mu.RLock()
	core := a.core
	a.mu.RUnlock()
	data, _ := json.Marshal(struct {
		Profile    *Profile
		Settings   Settings
		Selections map[string]map[string]string
		Core       string
		Running    bool
	}{findProfile(s, s.ActiveProfile), s.Settings, s.Selections, fmt.Sprintf("%p", core), core.Running()})
	return hashBytes(data)
}
func (a *App) connectivityState() map[string]any {
	signature := a.healthSignature()
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	reports := map[string]*HealthReport{}
	jobs := map[string]string{}
	stale := map[string]bool{}
	for key, report := range a.healthReports {
		reports[key] = report
		when, _ := time.Parse(time.RFC3339, report.FinishedAt)
		ttl := 10 * time.Minute
		if key == "media" {
			ttl = time.Hour
		}
		stale[key] = report.Signature != signature || time.Since(when) > ttl
	}
	for key, id := range a.healthJobs {
		jobs[key] = id
	}
	return map[string]any{"reports": reports, "jobs": jobs, "stale": stale, "sites": siteTargets, "media": mediaTargets}
}
func (a *App) beginConnectivity(kind string) (any, error) {
	signature := a.healthSignature()
	a.healthMu.Lock()
	defer a.healthMu.Unlock()
	if a.healthJobs == nil {
		a.healthJobs = map[string]string{}
	}
	// All browsers share a bounded single job, including basic vs media checks.
	for active, id := range a.healthJobs {
		if active != kind {
			return nil, fmt.Errorf("另一项网络检测正在进行，请稍后再试")
		}
		return map[string]string{"jobId": id}, nil
	}
	if old := a.healthReports[kind]; old != nil && old.Signature == signature {
		when, _ := time.Parse(time.RFC3339, old.FinishedAt)
		if time.Since(when) < 30*time.Second {
			return old, nil
		}
	}
	job := a.newJob("connectivity."+kind, func() (any, error) {
		defer func() { a.healthMu.Lock(); delete(a.healthJobs, kind); a.healthMu.Unlock() }()
		ctx, cancel := context.WithTimeout(a.operationContext(), 65*time.Second)
		defer cancel()
		report := a.runConnectivity(ctx, kind, signature)
		a.healthMu.Lock()
		if a.healthReports == nil {
			a.healthReports = map[string]*HealthReport{}
		}
		a.healthReports[kind] = report
		a.healthMu.Unlock()
		return report, nil
	})
	a.healthJobs[kind] = job["jobId"]
	return job, nil
}
func healthClient(proxy *url.URL) *http.Client {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 15 * time.Second}).DialContext, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 7 * time.Second, MaxIdleConns: 4, MaxIdleConnsPerHost: 1, DisableKeepAlives: true}
	if proxy != nil {
		transport.Proxy = http.ProxyURL(proxy)
	}
	return &http.Client{Transport: transport, Timeout: 9 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return fmt.Errorf("too many redirects")
		}
		// Follow same-service HTTPS redirects only; never turn a public probe into LAN access.
		if req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" {
			return http.ErrUseLastResponse
		}
		initial := via[0].URL.Hostname()
		next := req.URL.Hostname()
		base := strings.TrimPrefix(initial, "www.")
		if next != initial && next != base && !strings.HasSuffix(next, "."+base) {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}
func (a *App) runConnectivity(ctx context.Context, kind, signature string) *HealthReport {
	s := a.snapshot()
	running := a.isRunning()
	var proxy *url.URL
	route := "路由器直连（代理未运行）"
	if running {
		proxy = &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", s.Settings.MixedPort)}
		route = "mihomo · " + s.Settings.Mode + " 模式"
		if a.isPreview() {
			route += "（仅测速，未接管流量）"
		}
	}
	report := &HealthReport{StartedAt: time.Now().Format(time.RFC3339), Signature: signature, Route: route}
	targets := siteTargets
	if kind == "media" {
		targets = mediaTargets
	} else {
		lan, wan := interfaceHealth(ctx, s.Settings.LANInterface, a.dev)
		report.LAN = &lan
		report.WAN = &wan
	}
	direct, routed := healthClient(nil), healthClient(proxy)
	defer direct.CloseIdleConnections()
	defer routed.CloseIdleConnections()
	report.Results = make([]HealthResult, len(targets))
	tasks := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range tasks {
				client := routed
				if targets[i].Group == "direct" {
					client = direct
				}
				report.Results[i] = probeHealth(ctx, client, targets[i])
			}
		}()
	}
	for i := range targets {
		tasks <- i
	}
	close(tasks)
	wg.Wait()
	report.FinishedAt = time.Now().Format(time.RFC3339)
	return report
}
func probeHealth(ctx context.Context, client *http.Client, target HealthTarget) HealthResult {
	result := HealthResult{ID: target.ID, Name: target.Name, Group: target.Group, State: "failed", Detail: "未返回结果"}
	method := "HEAD"
	limit := int64(2048)
	if target.Parser != "" {
		method = "GET"
		limit = 512 << 10
	}
	started := time.Now()
	request := func(method string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, method, target.URL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CWClashConnectivity/0.3.9)")
		req.Header.Set("Accept-Language", "en-US,en;q=0.8")
		return client.Do(req)
	}
	response, err := request(method)
	if err == nil && method == "HEAD" && (response.StatusCode == 405 || response.StatusCode == 501) {
		response.Body.Close()
		response, err = request("GET")
	}
	result.LatencyMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Detail = healthError(err)
		return result
	}
	defer response.Body.Close()
	result.HTTP = response.StatusCode
	body, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	truncated := int64(len(body)) > limit
	if truncated {
		body = body[:limit]
	}
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		result.State = "reachable"
		result.Detail = "HTTPS 已连通"
	case response.StatusCode >= 300 && response.StatusCode < 400:
		result.State = "limited"
		result.Detail = "收到跳转，需浏览器确认"
	case response.StatusCode == 407:
		result.State = "limited"
		result.Detail = "本机代理要求认证，未验证目标站点"
	case response.StatusCode == 401 || response.StatusCode == 403 || response.StatusCode == 429 || response.StatusCode == 451:
		result.State = "limited"
		result.Detail = "站点已响应，但有认证、限流或访问限制"
	case response.StatusCode >= 500:
		result.State = "limited"
		result.Detail = "站点或上游返回服务错误"
	default:
		result.State = "limited"
		result.Detail = "站点已响应，测试地址未成功"
	}
	if target.Parser == "trace" && result.State == "reachable" {
		for _, line := range strings.Split(string(body), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			if k == "ip" && net.ParseIP(v) != nil {
				result.IP = v
			}
			if k == "loc" && countryCode.MatchString(v) {
				result.Region = v
			}
		}
		if result.IP == "" {
			result.State = "unknown"
			result.Detail = "出口接口未返回可识别的地址"
		}
	}
	if target.Group == "media" {
		classifyMedia(&result, target.Parser, string(body), response.Request.URL.Path, truncated || readErr != nil)
	}
	return result
}

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)
var mediaCountry = regexp.MustCompile(`"(?:countryCode|geoCountry)"\s*:\s*"([A-Z]{2})"`)
var netflixCountry = regexp.MustCompile(`"requestCountry"\s*:\s*\{\s*"id"\s*:\s*"([A-Z]{2})"`)

func classifyMedia(result *HealthResult, parser, body, path string, incomplete bool) {
	result.Unlock = "unknown"
	if result.State != "reachable" {
		return
	}
	lower := strings.ToLower(body)
	if strings.Contains(lower, "cf-chl-") || strings.Contains(lower, "captcha") || strings.Contains(lower, "verify you are human") {
		result.State = "limited"
		result.Detail = "检测遇到人机验证，无法判断解锁"
		return
	}
	if match := mediaCountry.FindStringSubmatch(body); len(match) > 1 {
		result.Region = match[1]
	}
	if parser == "netflix" {
		if match := netflixCountry.FindStringSubmatch(body); len(match) > 1 {
			result.Region = match[1]
		}
	}
	blocked := strings.Contains(lower, "not available in your country") || strings.Contains(lower, "not available in your region") || strings.Contains(lower, "unavailable in your location") || strings.Contains(lower, "forbidden-location")
	if parser == "disney" && (strings.Contains(path, "/unavailable") || strings.Contains(path, "/not-available")) {
		blocked = true
	}
	if blocked {
		result.Unlock = "restricted"
		result.Detail = "页面明确提示地区不可用"
		return
	}
	if parser == "netflix" && (strings.Contains(lower, `property="og:video"`) || strings.Contains(lower, `property='og:video'`)) {
		result.Unlock = "likely"
		result.Detail = "检测到片目视频信息，疑似可用；需账号播放确认"
		return
	}
	if parser == "youtube" && (strings.Contains(body, "purchaseButtonOverride") || strings.Contains(body, "Start trial")) {
		result.Unlock = "likely"
		result.Detail = "检测到 Premium 订购入口；不代表账号已订阅"
		return
	}
	if parser == "spotify" && result.Region != "" {
		result.Detail = "已识别站点地区；曲库与播放权限待账号验证"
		return
	}
	result.Detail = "页面可达；解锁状态需登录或实际播放确认"
	if incomplete {
		result.Detail = "页面读取不完整；未据此判断解锁"
	}
}
func healthError(err error) string {
	var dns *net.DNSError
	var certificate x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		return "检测已取消"
	case errors.As(err, &dns):
		return "DNS 解析失败"
	case errors.As(err, &certificate) || errors.As(err, &invalid) || errors.As(err, &hostname):
		return "TLS 证书检查失败"
	case errors.As(err, &network) && network.Timeout():
		return "连接或 TLS 握手超时"
	default:
		return "连接失败（目标或当前线路不可达）"
	}
}
func interfaceHealth(ctx context.Context, lan string, dev bool) (InterfaceHealth, InterfaceHealth) {
	left := readInterface(lan)
	right := InterfaceHealth{State: "unknown", Carrier: "unknown", Detail: "未识别 WAN 接口"}
	if dev || runtime.GOOS != "linux" {
		right.Detail = "本地开发环境不读取路由器 WAN"
		return left, right
	}
	get := func(key string) string {
		c, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		out, err := exec.CommandContext(c, "nvram", "get", key).Output()
		if err != nil || len(out) > 256 {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	unit := "0"
	if get("wan1_primary") == "1" {
		unit = "1"
	}
	prefix := "wan" + unit + "_"
	physical := get(prefix + "ifname")
	logical := physical
	proto := get(prefix + "proto")
	if proto == "pppoe" || proto == "pptp" || proto == "l2tp" {
		if ppp := get(prefix + "pppoe_ifname"); ppp != "" {
			logical = ppp
		}
	}
	right = readInterface(logical)
	right.Detail = "WAN" + unit + " · " + proto + " · " + right.Detail
	if physical != "" && physical != logical {
		right.Carrier = readCarrier(physical)
		right.Detail += " · 物理接口 " + physical
	}
	return left, right
}
func readCarrier(name string) string {
	if !validID(name) || len(name) > 15 {
		return "unknown"
	}
	b, err := os.ReadFile(filepath.Join("/sys/class/net", name, "carrier"))
	if err != nil {
		return "unknown"
	}
	if strings.TrimSpace(string(b)) == "1" {
		return "up"
	}
	if strings.TrimSpace(string(b)) == "0" {
		return "down"
	}
	return "unknown"
}
func readInterface(name string) InterfaceHealth {
	result := InterfaceHealth{Name: name, State: "unknown", Carrier: "unknown", Addresses: []string{}, Detail: "接口信息不可用"}
	if !validID(name) || len(name) > 15 {
		return result
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return result
	}
	result.Carrier = readCarrier(name)
	addrs, err := iface.Addrs()
	if err != nil {
		return result
	}
	for _, addr := range addrs {
		ip, _, err := net.ParseCIDR(addr.String())
		if err == nil && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() {
			result.Addresses = append(result.Addresses, addr.String())
		}
	}
	switch {
	case iface.Flags&net.FlagUp == 0:
		result.State = "down"
		result.Detail = "接口未启用"
	case result.Carrier == "down":
		result.State = "down"
		result.Detail = "链路未接通"
	case len(result.Addresses) == 0:
		result.State = "limited"
		result.Detail = "接口已启用，未取得可用地址"
	default:
		result.State = "ready"
		result.Detail = "接口已启用且已取得地址"
	}
	return result
}
