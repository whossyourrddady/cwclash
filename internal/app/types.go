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
	"sync"
	"time"
)

const (
	DefaultRoot    = "/koolshare/cwclash"
	DefaultRuntime = "/tmp/cwclash-run"
	DefaultListen  = "127.0.0.1:17921"
	MaxConfigSize  = 8 << 20
	MaxArchiveSize = 64 << 20
	MaxBinarySize  = 192 << 20
)

type Envelope struct {
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type Profile struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	URL              string `json:"url,omitempty"`
	UserAgent        string `json:"userAgent,omitempty"`
	UpdateHours      int    `json:"updateHours"`
	UpdatedAt        string `json:"updatedAt,omitempty"`
	SubscriptionInfo string `json:"subscriptionInfo,omitempty"`
	LastAttempt      string `json:"lastAttempt,omitempty"`
	UpdateError      string `json:"updateError,omitempty"`
	UpdateFailures   int    `json:"updateFailures,omitempty"`
	RetryAt          string `json:"retryAt,omitempty"`
}

type DevicePolicy struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	IP    string `json:"ip"`
	MAC   string `json:"mac,omitempty"`
	Mode  string `json:"mode"`
	Group string `json:"group,omitempty"`
}

type Settings struct {
	LANInterface        string   `json:"lanInterface"`
	MixedPort           int      `json:"mixedPort"`
	RedirPort           int      `json:"redirPort"`
	TProxyPort          int      `json:"tproxyPort"`
	DNSPort             int      `json:"dnsPort"`
	ControllerPort      int      `json:"controllerPort"`
	DNSMode             string   `json:"dnsMode"`
	IPv6                string   `json:"ipv6"`
	BlockQUIC           bool     `json:"blockQuic"`
	DefaultGroup        string   `json:"defaultGroup"`
	DirectDomains       []string `json:"directDomains"`
	ProxyDomains        []string `json:"proxyDomains"`
	DirectCIDRs         []string `json:"directCidrs"`
	ProxyCIDRs          []string `json:"proxyCidrs"`
	CustomRules         []string `json:"customRules"`
	OverrideYAML        string   `json:"overrideYaml"`
	Mode                string   `json:"mode"`
	LogLevel            string   `json:"logLevel"`
	GeoAutoUpdate       *bool    `json:"geoAutoUpdate,omitempty"`
	GeoUpdateHours      int      `json:"geoUpdateHours"`
	ProviderUpdateHours int      `json:"providerUpdateHours"`
}

type State struct {
	Schema        int                          `json:"schema"`
	Enabled       bool                         `json:"enabled"`
	ActiveProfile string                       `json:"activeProfile"`
	Profiles      []Profile                    `json:"profiles"`
	Devices       []DevicePolicy               `json:"devices"`
	Settings      Settings                     `json:"settings"`
	Kernel        KernelState                  `json:"kernel"`
	Selections    map[string]map[string]string `json:"selections,omitempty"`
}

type KernelState struct {
	Version       string       `json:"version"`
	File          string       `json:"file"`
	SHA256        string       `json:"sha256"`
	Compression   string       `json:"compression,omitempty"`
	ArchiveSHA256 string       `json:"archiveSHA256,omitempty"`
	Previous      *KernelState `json:"previous,omitempty"`
}

type Job struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt,omitempty"`
	Result     any    `json:"result,omitempty"`
}

type LogEntry struct {
	ID      uint64 `json:"id"`
	Time    string `json:"time"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

type DNSQuery struct {
	ID         uint64 `json:"id"`
	Time       string `json:"time"`
	Client     string `json:"client"`
	Name       string `json:"name"`
	Type       uint16 `json:"type"`
	RCODE      int    `json:"rcode"`
	DurationMS int64  `json:"durationMs"`
}

type App struct {
	ipv6TPROXYReady bool // guarded by mu; established only by startup preflight
	root            string
	run             string
	storageMode     string
	usb             *usbStorage
	listen          string
	version         string
	dev             bool
	mu              sync.RWMutex
	op              sync.Mutex
	state           State
	jobs            map[string]*Job
	profileTasks    map[string]context.CancelFunc
	validationSlot  chan struct{}
	healthMu        sync.Mutex
	healthReports   map[string]*HealthReport
	healthJobs      map[string]string
	logs            []LogEntry
	dnsQueries      []DNSQuery
	sequence        uint64
	core            *Core
	previewMode     bool // guarded by mu; volatile, never resumes traffic interception
	started         time.Time
	lastError       string
	token           string
	secret          string
	ctx             context.Context
	cancel          context.CancelFunc
	dns             *DNSProxy
	uploads         map[string]*Upload
	downloads       map[string]string
	lastUpdateCheck time.Time
	latestRelease   *Release
	leaseSignature  string
	asdBackup       *ASDCacheBackup
}

func Background() context.Context { return context.Background() }

func defaults() State {
	return State{Schema: 1, Profiles: []Profile{}, Devices: []DevicePolicy{}, Settings: Settings{
		LANInterface: "br0", MixedPort: 7890, RedirPort: 7892, TProxyPort: 7893,
		DNSPort: 1054, ControllerPort: 19090, DNSMode: "redir-host", IPv6: "auto",
		Mode: "rule", LogLevel: "info", DirectDomains: []string{}, ProxyDomains: []string{},
		DirectCIDRs: []string{}, ProxyCIDRs: []string{}, CustomRules: []string{},
	}}
}

func (a *App) snapshot() State {
	a.mu.RLock()
	defer a.mu.RUnlock()
	b, _ := json.Marshal(a.state)
	var result State
	_ = json.Unmarshal(b, &result)
	return result
}

func (a *App) persist(s State) error {
	if err := a.ensureStorage(); err != nil {
		return err
	}
	if s.Profiles == nil {
		s.Profiles = []Profile{}
	}
	if s.Devices == nil {
		s.Devices = []DevicePolicy{}
	}
	if err := validateState(s); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err = atomicWrite(filepath.Join(a.root, "state.json"), b, 0600); err != nil {
		return err
	}
	a.mu.Lock()
	a.state = s
	a.mu.Unlock()
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".cwclash-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func validID(v string) bool {
	if len(v) < 1 || len(v) > 80 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validateSettings(s Settings) error {
	if s.GeoUpdateHours < 0 || s.GeoUpdateHours > 720 || s.ProviderUpdateHours < 0 || s.ProviderUpdateHours > 720 {
		return fmt.Errorf("更新周期需为 0–720 小时；0 表示沿用配置")
	}
	if !validID(s.LANInterface) || len(s.LANInterface) > 15 {
		return fmt.Errorf("LAN 网桥名称无效")
	}
	ports := map[int]bool{}
	for _, p := range []int{s.MixedPort, s.RedirPort, s.TProxyPort, s.DNSPort, s.ControllerPort, 1053, 17921} {
		if p < 1024 || p > 65535 || ports[p] {
			return fmt.Errorf("端口需要互不重复，且在 1024–65535 之间")
		}
		ports[p] = true
	}
	if s.DNSMode != "fake-ip" && s.DNSMode != "redir-host" {
		return fmt.Errorf("DNS 模式无效")
	}
	if s.IPv6 != "auto" && s.IPv6 != "proxy" && s.IPv6 != "block" {
		return fmt.Errorf("IPv6 选项无效")
	}
	if s.Mode != "rule" && s.Mode != "global" && s.Mode != "direct" {
		return fmt.Errorf("代理模式无效")
	}
	if !contains([]string{"silent", "error", "warning", "info", "debug"}, s.LogLevel) {
		return fmt.Errorf("日志级别无效")
	}
	for _, list := range [][]string{s.DirectDomains, s.ProxyDomains} {
		for _, d := range list {
			if len(d) > 253 || strings.ContainsAny(d, "\r\n, /\\\t") || d == "" {
				return fmt.Errorf("域名格式无效：%q", d)
			}
		}
	}
	for _, list := range [][]string{s.DirectCIDRs, s.ProxyCIDRs} {
		for _, cidr := range list {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return fmt.Errorf("CIDR 格式无效：%s", cidr)
			}
		}
	}
	if len(s.OverrideYAML) > MaxConfigSize {
		return fmt.Errorf("配置覆写过大")
	}
	return nil
}

func contains(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

func validateState(s State) error {
	if s.Schema != 1 {
		return fmt.Errorf("配置版本不受支持")
	}
	if err := validateSettings(s.Settings); err != nil {
		return err
	}
	ids := map[string]bool{}
	for _, p := range s.Profiles {
		if !validID(p.ID) || ids[p.ID] {
			return fmt.Errorf("配置标识无效或重复")
		}
		ids[p.ID] = true
		if strings.TrimSpace(p.Name) == "" || len(p.Name) > 100 {
			return fmt.Errorf("配置名称无效")
		}
		if p.UpdateHours < 0 || p.UpdateHours > 720 {
			return fmt.Errorf("订阅更新间隔无效")
		}
		if p.URL != "" {
			if err := validHTTPURL(p.URL); err != nil {
				return err
			}
		}
		if len(p.UserAgent) > 256 || strings.ContainsAny(p.UserAgent, "\r\n") {
			return fmt.Errorf("订阅 User-Agent 无效")
		}
	}
	if s.ActiveProfile != "" && !ids[s.ActiveProfile] {
		return fmt.Errorf("当前配置不存在")
	}
	for id, groups := range s.Selections {
		if !ids[id] || len(groups) > 512 {
			return fmt.Errorf("代理组选择记录无效")
		}
		for group, node := range groups {
			if group == "" || node == "" || len(group) > 512 || len(node) > 512 || strings.ContainsAny(group+node, "\x00\r\n") {
				return fmt.Errorf("代理组选择记录无效")
			}
		}
	}
	if len(s.Devices) > 256 {
		return fmt.Errorf("设备规则过多")
	}
	ids = map[string]bool{}
	for _, d := range s.Devices {
		if !validID(d.ID) || ids[d.ID] {
			return fmt.Errorf("设备标识无效或重复")
		}
		ids[d.ID] = true
		if net.ParseIP(d.IP) == nil || !contains([]string{"rule", "direct", "proxy"}, d.Mode) {
			return fmt.Errorf("设备地址或模式无效")
		}
		if d.MAC != "" {
			if _, err := net.ParseMAC(d.MAC); err != nil {
				return fmt.Errorf("设备 MAC 地址无效")
			}
		}
	}
	return nil
}

func (a *App) log(level, msg string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sequence++
	a.logs = append(a.logs, LogEntry{ID: a.sequence, Time: time.Now().Format(time.RFC3339), Level: level, Message: redact(msg)})
	if len(a.logs) > 2000 {
		a.logs = append([]LogEntry(nil), a.logs[len(a.logs)-2000:]...)
	}
}
