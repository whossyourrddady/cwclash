// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	ProxyMark    = 0x910000
	OutboundMark = 0x920000
	MarkMask     = 0xff0000
	RouteTable   = 202
	RulePriority = 20210
)

type Command struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}
type netOwnership struct {
	IPv4 bool `json:"ipv4"`
	IPv6 bool `json:"ipv6"`
}

func commandOutput(program string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, program, args...).Output()
	if err != nil {
		return ""
	}
	if len(b) > 1<<20 {
		return ""
	}
	return string(b)
}

func runCommand(c Command) error {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, c.Program, c.Args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s 失败：%s", c.Program, strings.Join(c.Args, " "), strings.TrimSpace(string(b)))
	}
	return nil
}

func Probe() map[string]any {
	interfaces := []map[string]any{}
	all, _ := net.Interfaces()
	for _, iface := range all {
		addresses, _ := iface.Addrs()
		ips := []string{}
		for _, ip := range addresses {
			ips = append(ips, ip.String())
		}
		interfaces = append(interfaces, map[string]any{"name": iface.Name, "addresses": ips, "up": iface.Flags&net.FlagUp != 0})
	}
	commands := map[string]bool{}
	for _, name := range []string{"iptables", "ip6tables", "ip", "dbus", "nvram", "mount", "mke2fs", "mkfs.ext4"} {
		_, err := exec.LookPath(name)
		commands[name] = err == nil
	}
	mounts := []string{}
	if b, err := os.ReadFile("/proc/mounts"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			parts := strings.Fields(line)
			if len(parts) > 2 && (parts[1] == "/jffs" || strings.HasPrefix(parts[1], "/tmp/mnt/")) {
				mounts = append(mounts, strings.Join(parts[:4], " "))
			}
		}
	}
	return map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "kernel": strings.TrimSpace(commandOutput("uname", "-r")), "model": strings.TrimSpace(commandOutput("nvram", "get", "productid")), "interfaces": interfaces, "commands": commands, "mounts": mounts}
}

func (a *App) ipv6Available() bool {
	if a.dev {
		return false
	}
	iface, err := net.InterfaceByName(a.snapshot().Settings.LANInterface)
	if err != nil {
		return false
	}
	addresses, _ := iface.Addrs()
	for _, addr := range addresses {
		ip, _, err := net.ParseCIDR(addr.String())
		if err == nil && ip.To4() == nil && ip.IsGlobalUnicast() {
			return true
		}
	}
	return false
}

func (a *App) ipv6ProxyCapable() bool {
	if a.dev {
		return false
	}
	if _, err := exec.LookPath("ip6tables"); err != nil {
		return false
	}
	help := commandOutput("ip6tables", "-t", "mangle", "-j", "TPROXY", "-h")
	if !strings.Contains(help, "--on-port") {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "ip6tables", "-t", "nat", "-S").Run() == nil
}

func (a *App) networkSummary(s State) map[string]any {
	mode := "未启用 IPv6"
	if a.ipv6Available() {
		mode = "IPv6 防绕过"
		if s.Settings.IPv6 != "block" && a.ipv6ProxyCapable() {
			mode = "IPv6 透明代理"
		}
	}
	return map[string]any{"lanInterface": s.Settings.LANInterface, "ipv6": mode, "dnsMode": s.Settings.DNSMode, "failOpen": true, "mixedPort": s.Settings.MixedPort}
}

func (a *App) preflightNetwork(s State) error {
	if err := validateSettings(s.Settings); err != nil {
		return err
	}
	iface, err := net.InterfaceByName(s.Settings.LANInterface)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return fmt.Errorf("LAN 网桥 %s 不存在或未启用", s.Settings.LANInterface)
	}
	for _, name := range []string{"iptables", "ip"} {
		if _, err = exec.LookPath(name); err != nil {
			return fmt.Errorf("固件缺少 %s", name)
		}
	}
	if !strings.Contains(commandOutput("iptables", "-t", "mangle", "-j", "TPROXY", "-h"), "--on-port") {
		return fmt.Errorf("固件缺少 TPROXY 用户态模块，无法启用 TCP/UDP 透明代理")
	}
	if a.ipv6Available() {
		if _, err := exec.LookPath("ip6tables"); err != nil {
			return fmt.Errorf("局域网已启用 IPv6，但固件缺少 ip6tables，无法保证受控设备不绕过代理")
		}
		if s.Settings.IPv6 == "proxy" && !a.ipv6ProxyCapable() {
			return fmt.Errorf("固件不具备完整 IPv6 TPROXY/DNS 接管能力，请选择自动防绕过")
		}
	}
	owned := a.readOwnership()
	for _, family := range []struct {
		flag  string
		owned bool
	}{{"-4", owned.IPv4}, {"-6", owned.IPv6}} {
		if family.flag == "-6" && !a.ipv6Available() {
			continue
		}
		routes := strings.TrimSpace(commandOutput("ip", family.flag, "route", "show", "table", strconv.Itoa(RouteTable)))
		foreignRoute := false
		for _, route := range strings.Split(routes, "\n") {
			if strings.TrimSpace(route) != "" && (!family.owned || !isOwnedLocalRoute(route)) {
				foreignRoute = true
			}
		}
		if foreignRoute {
			return fmt.Errorf("路由表 %d 已被其他服务占用，未修改其规则", RouteTable)
		}
		if !family.owned && ownedPolicyRulePresent(commandOutput("ip", family.flag, "rule", "show")) {
			return fmt.Errorf("策略规则 %d 已存在但缺少本插件所有权记录，未修改其规则", RulePriority)
		}
	}
	return nil
}

func (a *App) applyNetwork(s State) error {
	if a.dev {
		return nil
	}
	if err := a.cleanupNetwork(); err != nil {
		return err
	}
	v6 := a.ipv6Available()
	v6proxy := v6 && s.Settings.IPv6 != "block" && a.ipv6ProxyCapable()
	own := netOwnership{IPv4: s.Settings.Mode != "direct", IPv6: s.Settings.Mode != "direct" && v6proxy}
	b, _ := json.Marshal(own)
	if err := atomicWrite(filepath.Join(a.run, "network-owned.json"), b, 0600); err != nil {
		return err
	}
	for _, command := range buildNetworkPlan(s, v6, v6proxy) {
		if err := runCommand(command); err != nil {
			_ = a.cleanupNetwork()
			return err
		}
	}
	return nil
}

func buildNetworkPlan(s State, ipv6, ipv6proxy bool) []Command {
	commands := []Command{}
	add := func(program string, args ...string) { commands = append(commands, Command{program, args}) }
	ipt := func(program, table string, args ...string) { add(program, append([]string{"-t", table}, args...)...) }
	mark := fmt.Sprintf("0x%x/0x%x", ProxyMark, MarkMask)
	outbound := fmt.Sprintf("0x%x/0x%x", OutboundMark, MarkMask)
	ports := []int{s.Settings.MixedPort, s.Settings.RedirPort, s.Settings.TProxyPort, s.Settings.DNSPort}
	for _, program := range []string{"iptables", "ip6tables"} {
		if program == "ip6tables" && !ipv6 {
			continue
		}
		ipt(program, "filter", "-N", "CWCLASH_IN")
		ipt(program, "filter", "-A", "CWCLASH_IN", "-i", "lo", "-j", "RETURN")
		ipt(program, "filter", "-A", "CWCLASH_IN", "-i", s.Settings.LANInterface, "-j", "RETURN")
		for _, port := range ports {
			for _, proto := range []string{"tcp", "udp"} {
				ipt(program, "filter", "-A", "CWCLASH_IN", "-p", proto, "--dport", strconv.Itoa(port), "-j", "DROP")
			}
		}
		ipt(program, "filter", "-I", "INPUT", "1", "-j", "CWCLASH_IN")
	}
	if s.Settings.Mode == "direct" {
		return commands
	}
	add("ip", "-4", "rule", "add", "pref", strconv.Itoa(RulePriority), "fwmark", mark, "table", strconv.Itoa(RouteTable))
	add("ip", "-4", "route", "add", "local", "0.0.0.0/0", "dev", "lo", "table", strconv.Itoa(RouteTable))
	ipt("iptables", "nat", "-N", "CWCLASH_NAT")
	ipt("iptables", "mangle", "-N", "CWCLASH_UDP")
	for _, chain := range []struct{ table, name string }{{"nat", "CWCLASH_NAT"}, {"mangle", "CWCLASH_UDP"}} {
		ipt("iptables", chain.table, "-A", chain.name, "-m", "mark", "--mark", outbound, "-j", "RETURN")
		if chain.table == "mangle" {
			ipt("iptables", chain.table, "-A", chain.name, "-p", "udp", "--dport", "53", "-j", "RETURN")
		}
		// Old fake-IP cache entries must still be translated after a device switches to DIRECT.
		if s.Settings.DNSMode == "fake-ip" {
			if chain.table == "nat" {
				ipt("iptables", chain.table, "-A", chain.name, "-d", "198.18.0.0/16", "-p", "tcp", "-j", "REDIRECT", "--to-ports", strconv.Itoa(s.Settings.RedirPort))
			} else {
				ipt("iptables", chain.table, "-A", chain.name, "-d", "198.18.0.0/16", "-p", "udp", "-j", "TPROXY", "--on-port", strconv.Itoa(s.Settings.TProxyPort), "--tproxy-mark", mark)
			}
		}
		for _, device := range s.Devices {
			if device.Mode == "direct" && net.ParseIP(device.IP).To4() != nil {
				ipt("iptables", chain.table, "-A", chain.name, "-s", device.IP, "-j", "RETURN")
			}
		}
		if chain.table == "nat" {
			for _, proto := range []string{"tcp", "udp"} {
				ipt("iptables", "nat", "-A", chain.name, "-p", proto, "--dport", "53", "-j", "REDIRECT", "--to-ports", strconv.Itoa(s.Settings.DNSPort))
			}
		}
		for _, cidr := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "224.0.0.0/4", "240.0.0.0/4"} {
			ipt("iptables", chain.table, "-A", chain.name, "-d", cidr, "-j", "RETURN")
		}
	}
	ipt("iptables", "nat", "-A", "CWCLASH_NAT", "-p", "tcp", "-j", "REDIRECT", "--to-ports", strconv.Itoa(s.Settings.RedirPort))
	ipt("iptables", "mangle", "-A", "CWCLASH_UDP", "-p", "udp", "-j", "TPROXY", "--on-port", strconv.Itoa(s.Settings.TProxyPort), "--tproxy-mark", mark)
	ipt("iptables", "nat", "-I", "PREROUTING", "1", "-i", s.Settings.LANInterface, "-j", "CWCLASH_NAT")
	ipt("iptables", "mangle", "-I", "PREROUTING", "1", "-i", s.Settings.LANInterface, "-j", "CWCLASH_UDP")
	if ipv6 {
		ipt("ip6tables", "filter", "-N", "CWCLASH6_FWD")
		for _, device := range s.Devices {
			if device.Mode != "direct" {
				continue
			}
			args := []string{"-A", "CWCLASH6_FWD"}
			if net.ParseIP(device.IP).To4() == nil {
				args = append(args, "-s", device.IP)
			} else {
				continue
			}
			ipt("ip6tables", "filter", append(args, "-j", "RETURN")...)
		}
		for _, cidr := range []string{"fe80::/10", "fc00::/7", "ff00::/8"} {
			ipt("ip6tables", "filter", "-A", "CWCLASH6_FWD", "-d", cidr, "-j", "RETURN")
		}
		if !ipv6proxy {
			ipt("ip6tables", "filter", "-A", "CWCLASH6_FWD", "-j", "REJECT")
		}
		ipt("ip6tables", "filter", "-I", "FORWARD", "1", "-i", s.Settings.LANInterface, "-j", "CWCLASH6_FWD")
	}
	if ipv6proxy {
		add("ip", "-6", "rule", "add", "pref", strconv.Itoa(RulePriority), "fwmark", mark, "table", strconv.Itoa(RouteTable))
		add("ip", "-6", "route", "add", "local", "::/0", "dev", "lo", "table", strconv.Itoa(RouteTable))
		ipt("ip6tables", "nat", "-N", "CWCLASH6_DNS")
		ipt("ip6tables", "mangle", "-N", "CWCLASH6")
		for _, chain := range []struct{ table, name string }{{"nat", "CWCLASH6_DNS"}, {"mangle", "CWCLASH6"}} {
			ipt("ip6tables", chain.table, "-A", chain.name, "-m", "mark", "--mark", outbound, "-j", "RETURN")
			if chain.table == "mangle" {
				for _, proto := range []string{"tcp", "udp"} {
					ipt("ip6tables", "mangle", "-A", chain.name, "-p", proto, "--dport", "53", "-j", "RETURN")
				}
				if s.Settings.DNSMode == "fake-ip" {
					for _, proto := range []string{"tcp", "udp"} {
						ipt("ip6tables", "mangle", "-A", chain.name, "-d", "fc00:198:18::/64", "-p", proto, "-j", "TPROXY", "--on-port", strconv.Itoa(s.Settings.TProxyPort), "--tproxy-mark", mark)
					}
				}
			}
			for _, device := range s.Devices {
				if device.Mode != "direct" {
					continue
				}
				args := []string{"-A", chain.name}
				if net.ParseIP(device.IP).To4() == nil {
					args = append(args, "-s", device.IP)
				} else {
					continue
				}
				ipt("ip6tables", chain.table, append(args, "-j", "RETURN")...)
			}
			if chain.table == "nat" {
				for _, proto := range []string{"tcp", "udp"} {
					ipt("ip6tables", "nat", "-A", chain.name, "-p", proto, "--dport", "53", "-j", "REDIRECT", "--to-ports", strconv.Itoa(s.Settings.DNSPort))
				}
			}
			if chain.table == "mangle" {
				for _, cidr := range []string{"::1/128", "fe80::/10", "fc00::/7", "ff00::/8"} {
					ipt("ip6tables", "mangle", "-A", chain.name, "-d", cidr, "-j", "RETURN")
				}
				for _, proto := range []string{"tcp", "udp"} {
					ipt("ip6tables", "mangle", "-A", chain.name, "-p", proto, "-j", "TPROXY", "--on-port", strconv.Itoa(s.Settings.TProxyPort), "--tproxy-mark", mark)
				}
			}
		}
		ipt("ip6tables", "nat", "-I", "PREROUTING", "1", "-i", s.Settings.LANInterface, "-j", "CWCLASH6_DNS")
		ipt("ip6tables", "mangle", "-I", "PREROUTING", "1", "-i", s.Settings.LANInterface, "-j", "CWCLASH6")
	}
	if s.Settings.BlockQUIC {
		for _, program := range []string{"iptables", "ip6tables"} {
			if program == "ip6tables" && !ipv6proxy {
				continue
			}
			ipt(program, "filter", "-N", "CWCLASH_QUIC")
			for _, device := range s.Devices {
				if device.Mode == "direct" {
					ip := net.ParseIP(device.IP)
					if (program == "iptables" && ip.To4() != nil) || (program == "ip6tables" && ip.To4() == nil) {
						ipt(program, "filter", "-A", "CWCLASH_QUIC", "-s", device.IP, "-j", "RETURN")
					}
				}
			}
			ipt(program, "filter", "-A", "CWCLASH_QUIC", "-p", "udp", "--dport", "443", "-j", "REJECT")
			// TPROXY traffic enters INPUT, while bypassed traffic enters FORWARD.
			for _, hook := range []string{"INPUT", "FORWARD"} {
				ipt(program, "filter", "-I", hook, "1", "-i", s.Settings.LANInterface, "-j", "CWCLASH_QUIC")
			}
		}
	}
	return commands
}

func (a *App) readOwnership() netOwnership {
	var own netOwnership
	b, _ := os.ReadFile(filepath.Join(a.run, "network-owned.json"))
	_ = json.Unmarshal(b, &own)
	return own
}

func ownedPolicyRulePresent(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != strconv.Itoa(RulePriority)+":" {
			continue
		}
		markOK, tableOK := false, false
		for i := 1; i < len(fields)-1; i++ {
			if fields[i] == "fwmark" {
				parts := strings.Split(fields[i+1], "/")
				if len(parts) == 2 {
					mark, err1 := strconv.ParseUint(parts[0], 0, 32)
					mask, err2 := strconv.ParseUint(parts[1], 0, 32)
					markOK = err1 == nil && err2 == nil && mark == ProxyMark && mask == MarkMask
				}
			}
			if (fields[i] == "lookup" || fields[i] == "table") && fields[i+1] == strconv.Itoa(RouteTable) {
				tableOK = true
			}
		}
		if markOK && tableOK {
			return true
		}
	}
	return false
}

func isOwnedLocalRoute(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[0] != "local" || !contains([]string{"default", "0.0.0.0/0", "::/0"}, fields[1]) {
		return false
	}
	for i := 2; i < len(fields)-1; i++ {
		if fields[i] == "dev" && fields[i+1] == "lo" {
			return true
		}
	}
	return false
}

func ipOutput(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "ip", args...).CombinedOutput()
	if len(b) > 1<<20 {
		return "", fmt.Errorf("ip 输出超过限制")
	}
	return strings.TrimSpace(string(b)), err
}

func (a *App) cleanupNetwork() error {
	if a.dev {
		return nil
	}
	var failures []string
	for _, program := range []string{"iptables", "ip6tables"} {
		if _, err := exec.LookPath(program); err != nil {
			continue
		}
		for _, table := range []string{"nat", "mangle", "filter"} {
			rules := strings.Split(commandOutput(program, "-t", table, "-S"), "\n")
			for _, rule := range rules {
				parts := strings.Fields(rule)
				if len(parts) < 3 || parts[0] != "-A" {
					continue
				}
				ownedJump := false
				for i := 0; i < len(parts)-1; i++ {
					if (parts[i] == "-j" || parts[i] == "-g") && strings.HasPrefix(parts[i+1], "CWCLASH") {
						ownedJump = true
					}
				}
				if ownedJump && !strings.HasPrefix(parts[1], "CWCLASH") {
					parts[0] = "-D"
					if err := runCommand(Command{program, append([]string{"-t", table}, parts...)}); err != nil {
						failures = append(failures, err.Error())
					}
				}
			}
			for _, rule := range rules {
				parts := strings.Fields(rule)
				if len(parts) == 2 && parts[0] == "-N" && strings.HasPrefix(parts[1], "CWCLASH") {
					if err := runCommand(Command{program, []string{"-t", table, "-F", parts[1]}}); err != nil {
						failures = append(failures, err.Error())
					}
					if err := runCommand(Command{program, []string{"-t", table, "-X", parts[1]}}); err != nil {
						failures = append(failures, err.Error())
					}
				}
			}
		}
	}
	own := a.readOwnership()
	mark := fmt.Sprintf("0x%x/0x%x", ProxyMark, MarkMask)
	for _, f := range []struct {
		flag, route string
		owned       bool
	}{{"-4", "0.0.0.0/0", own.IPv4}, {"-6", "::/0", own.IPv6}} {
		if !f.owned {
			continue
		}
		// Partial setup or a firewall restart may already have removed these entries.
		// Verify absence instead of silently discarding deletion failures.
		for attempt := 0; attempt < 8; attempt++ {
			rules, err := ipOutput(f.flag, "rule", "show")
			if err != nil || !ownedPolicyRulePresent(rules) {
				break
			}
			if err = runCommand(Command{"ip", []string{f.flag, "rule", "del", "pref", strconv.Itoa(RulePriority), "fwmark", mark, "table", strconv.Itoa(RouteTable)}}); err != nil {
				break
			}
		}
		rules, ruleErr := ipOutput(f.flag, "rule", "show")
		if ruleErr != nil || ownedPolicyRulePresent(rules) {
			failures = append(failures, f.flag+" 策略规则未确认清理，保留所有权记录")
		}
		_ = runCommand(Command{"ip", []string{f.flag, "route", "del", "local", f.route, "dev", "lo", "table", strconv.Itoa(RouteTable)}})
		routes, routeErr := ipOutput(f.flag, "route", "show", "table", strconv.Itoa(RouteTable))
		missingTable := strings.Contains(routes, "FIB table does not exist") || strings.Contains(routes, "No such process")
		if routeErr != nil && !missingTable {
			failures = append(failures, f.flag+" 路由表未确认清理，保留所有权记录")
		}
		for _, route := range strings.Split(routes, "\n") {
			if isOwnedLocalRoute(route) {
				failures = append(failures, f.flag+" 本地代理路由仍存在，保留所有权记录")
				break
			}
		}
	}
	if len(failures) == 0 {
		_ = os.Remove(filepath.Join(a.run, "network-owned.json"))
	} else {
		return fmt.Errorf("%s", strings.Join(failures, "；"))
	}
	return nil
}

type ConnectedDevice struct {
	Name   string `json:"name"`
	IP     string `json:"ip"`
	MAC    string `json:"mac"`
	Policy string `json:"policy,omitempty"`
}

func connectedDevices(policies []DevicePolicy) []ConnectedDevice {
	out := []ConnectedDevice{}
	seen := map[string]bool{}
	for _, path := range []string{"/var/lib/misc/dnsmasq.leases", "/tmp/var/lib/misc/dnsmasq.leases"} {
		if b, err := os.ReadFile(path); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				p := strings.Fields(line)
				if len(p) < 4 || net.ParseIP(p[2]) == nil || seen[p[2]] {
					continue
				}
				name := p[3]
				if name == "*" {
					name = ""
				}
				out = append(out, ConnectedDevice{Name: name, IP: p[2], MAC: p[1]})
				seen[p[2]] = true
			}
		}
	}
	for _, line := range strings.Split(commandOutput("ip", "neigh", "show"), "\n") {
		p := strings.Fields(line)
		if len(p) < 5 || net.ParseIP(p[0]) == nil || seen[p[0]] {
			continue
		}
		mac := ""
		for i := 0; i < len(p)-1; i++ {
			if p[i] == "lladdr" {
				mac = p[i+1]
			}
		}
		if mac != "" {
			out = append(out, ConnectedDevice{IP: p[0], MAC: mac})
			seen[p[0]] = true
		}
	}
	for i := range out {
		for _, p := range policies {
			if p.IP == out[i].IP {
				out[i].Policy = p.Mode
				if out[i].Name == "" {
					out[i].Name = p.Name
				}
			}
		}
	}
	return out
}
