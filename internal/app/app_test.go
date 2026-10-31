// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const testYAML = `proxies:
  - name: alpha
    type: socks5
    server: 127.0.0.1
    port: 1080
proxy-groups:
  - name: Route
    type: select
    proxies: [alpha, DIRECT]
rules:
  - MATCH,Route
`

func testApp(t *testing.T) *App {
	t.Helper()
	base := t.TempDir()
	a, err := New(filepath.Join(base, "root"), filepath.Join(base, "run"), "127.0.0.1:0", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func decodeMap(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCompilePreservesNodesAndOwnsRouterListeners(t *testing.T) {
	a := testApp(t)
	s := a.snapshot()
	raw := testYAML + `external-controller: 0.0.0.0:9999
external-controller-unix: /tmp/exposed.sock
external-ui-url: https://untrusted.invalid/ui.zip
listeners: [{name: public, type: mixed, port: 8080}]
tun: {enable: true, auto-route: true}
`
	b, err := a.compileConfig(s, []byte(raw), 19090)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, b)
	if m["external-controller"] != "127.0.0.1:19090" || m["secret"] != a.secret {
		t.Fatal("controller escaped router ownership")
	}
	if _, ok := m["external-controller-unix"]; ok {
		t.Fatal("subscription exposed unix controller")
	}
	if _, ok := m["external-ui-url"]; ok {
		t.Fatal("subscription exposed external UI download")
	}
	if len(asList(m["listeners"])) != 0 || asMap(m["tun"])["enable"] != false {
		t.Fatal("subscription enabled an unmanaged inbound")
	}
	if len(asList(m["proxies"])) != 1 || asMap(asList(m["proxies"])[0])["name"] != "alpha" {
		t.Fatal("lost subscription node")
	}
}

func TestRulePriorityAndStructuredOverwrite(t *testing.T) {
	a := testApp(t)
	s := a.snapshot()
	s.Devices = []DevicePolicy{{ID: "device", IP: "192.168.50.20", Mode: "direct"}}
	s.Settings.DirectDomains = []string{"local.example"}
	s.Settings.ProxyDomains = []string{"remote.example"}
	s.Settings.CustomRules = []string{"DOMAIN,custom.example,Route"}
	s.Settings.OverrideYAML = "dns:\n  nameserver: [tls://1.1.1.1]\nunified-delay: true\n"
	b, err := a.compileConfig(s, []byte(testYAML), 19090)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeMap(t, b)
	rules := asList(m["rules"])
	want := []string{"SRC-IP-CIDR,192.168.50.20/32,DIRECT", "DOMAIN-SUFFIX,local.example,DIRECT", "DOMAIN-SUFFIX,remote.example,Route", "DOMAIN,custom.example,Route", "MATCH,Route"}
	for i, v := range want {
		if rules[i] != v {
			t.Fatalf("rule %d: got %v want %s", i, rules[i], v)
		}
	}
	if asList(asMap(m["dns"])["nameserver"])[0] != "tls://1.1.1.1" || m["unified-delay"] != true {
		t.Fatal("structured override lost")
	}
}

func TestMissingGroupIsRejected(t *testing.T) {
	a := testApp(t)
	s := a.snapshot()
	s.Settings.DefaultGroup = "missing"
	if _, err := a.compileConfig(s, []byte(testYAML), 19090); err == nil {
		t.Fatal("missing group accepted")
	}
}

func TestIPv6SourcePolicyUsesSupportedMihomoRule(t *testing.T) {
	a := testApp(t)
	s := a.snapshot()
	s.Devices = []DevicePolicy{{ID: "v6", IP: "2001:db8::2", Mode: "direct"}}
	b, err := a.compileConfig(s, []byte(testYAML), 19090)
	if err != nil {
		t.Fatal(err)
	}
	if asList(decodeMap(t, b)["rules"])[0] != "SRC-IP-CIDR,2001:db8::2/128,DIRECT" {
		t.Fatal("IPv6 source rules must use SRC-IP-CIDR, not an unsupported SRC-IP-CIDR6 type")
	}
}

func TestIPv6FakeRangeInterceptPrecedesULABYPASS(t *testing.T) {
	s := defaults()
	s.Settings.DNSMode = "fake-ip"
	var lines []string
	for _, c := range buildNetworkPlan(s, true, true) {
		lines = append(lines, c.Program+" "+strings.Join(c.Args, " "))
	}
	v := strings.Join(lines, "\n")
	fake := strings.Index(v, "-A CWCLASH6 -d fc00:198:18::/64")
	ula := strings.Index(v, "-A CWCLASH6 -d fc00::/7")
	if fake < 0 || ula < 0 || fake > ula {
		t.Fatal("fake IPv6 addresses bypass the core")
	}
}
func TestYAMLRejectsDuplicateAndMultipleDocuments(t *testing.T) {
	for _, raw := range []string{"proxies: []\nproxies: []\n", testYAML + "---\nrules: [MATCH,DIRECT]\n", "hello world", "rules: [MATCH,DIRECT]\n---\n"} {
		if _, err := parseYAML([]byte(raw)); err == nil {
			t.Errorf("accepted invalid document %q", raw)
		}
	}
}

func TestProviderPathsStayWithinPlugin(t *testing.T) {
	a := testApp(t)
	raw := testYAML + "proxy-providers:\n  cloud:\n    type: http\n    url: https://example.invalid/provider.yaml\n    path: /jffs/other-plugin/overwrite\n"
	b, err := a.compileConfig(a.snapshot(), []byte(raw), 19090)
	if err != nil {
		t.Fatal(err)
	}
	path := asMap(asMap(decodeMap(t, b)["proxy-providers"])["cloud"])["path"].(string)
	if !strings.HasPrefix(path, filepath.Join(a.run, "data", "providers")) {
		t.Fatal(path)
	}
	for _, bad := range []string{"../outside", "/etc/passwd", "C:\\outside", "a/../../out", "a\x00b"} {
		if _, err := safeRelative(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestFailedProfileUpdateKeepsSourceAndState(t *testing.T) {
	a := testApp(t)
	p, err := a.saveProfile(context.Background(), Profile{Name: "original", UpdateHours: 24}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.saveProfile(context.Background(), Profile{ID: p.ID, Name: "broken"}, "proxies: [broken\n")
	if err == nil {
		t.Fatal("accepted broken update")
	}
	b, _ := os.ReadFile(filepath.Join(a.root, "profiles", p.ID+".yaml"))
	if string(b) != testYAML || a.snapshot().Profiles[0].Name != "original" {
		t.Fatal("failed update changed active data")
	}
}

func TestPersistenceDoesNotPublishFailedWrite(t *testing.T) {
	a := testApp(t)
	before := a.snapshot()
	path := filepath.Join(a.root, "state.json")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	next := before
	next.Enabled = true
	if err := a.persist(next); err == nil {
		t.Fatal("expected rename failure")
	}
	if a.snapshot().Enabled {
		t.Fatal("published uncommitted state")
	}
}

func TestModeCommitFailureRestoresCoreAndStoredMode(t *testing.T) {
	a := testApp(t)
	var modes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q map[string]string
		if r.Method != "PATCH" || r.URL.Path != "/configs" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		modes = append(modes, q["mode"])
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(u.Port())
	s := a.snapshot()
	s.Settings.ControllerPort = port
	if err := a.persist(s); err != nil {
		t.Fatal(err)
	}
	a.core = &Core{running: true}
	path := filepath.Join(a.root, "state.json")
	if err := os.Rename(path, path+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := a.setMode(context.Background(), "direct"); err == nil {
		t.Fatal("expected state commit failure")
	}
	if strings.Join(modes, ",") != "direct,rule" || a.snapshot().Settings.Mode != "rule" {
		t.Fatal("failed mode change was not rolled back", modes)
	}
}

func TestHTTPRequiresLocalTokenAndKnownAction(t *testing.T) {
	a := testApp(t)
	handler := a.Handler()
	body := `{"action":"state.get","payload":{}}`
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/rpc", strings.NewReader(body)))
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/rpc", strings.NewReader(`{"action":"exec","payload":{"command":"anything"}}`))
	r.Header.Set("X-CWClash-Token", a.token)
	handler.ServeHTTP(w, r)
	var envelope Envelope
	_ = json.Unmarshal(w.Body.Bytes(), &envelope)
	if envelope.OK || !strings.Contains(envelope.Error, "不支持") {
		t.Fatal(w.Body.String())
	}
}

func TestIPCRejectsNonLoopbackDestination(t *testing.T) {
	run := t.TempDir()
	_ = os.WriteFile(filepath.Join(run, "ipc.json"), []byte(`{"address":"192.0.2.1:80","token":"x"}`), 0600)
	result := Call(run, "status", "e30=")
	if result.OK || !strings.Contains(result.Error, "非本机") {
		t.Fatal(result)
	}
}

func waitTestJob(t *testing.T, a *App, result any) *Job {
	t.Helper()
	id := result.(map[string]string)["jobId"]
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.RLock()
		copy := *a.jobs[id]
		a.mu.RUnlock()
		if copy.Status != "running" {
			return &copy
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job timed out")
	return nil
}

func TestAsyncSubscriptionSurvivesRequestCancellation(t *testing.T) {
	a := testApp(t)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		io.WriteString(w, testYAML)
	}))
	defer source.Close()
	ctx, cancel := context.WithCancel(context.Background())
	payload, _ := json.Marshal(map[string]any{"name": "online", "url": source.URL, "updateHours": 24})
	result, err := a.dispatch(ctx, "profiles.save", payload)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	job := waitTestJob(t, a, result)
	if job.Status != "done" {
		t.Fatal(job.Message)
	}
}

func TestUploadRejectsReplaysAndHashMismatch(t *testing.T) {
	a := testApp(t)
	p, _ := json.Marshal(map[string]any{"kind": "profile", "name": "test.yaml", "size": 3, "sha256": strings.Repeat("0", 64)})
	result, err := a.handleUpload(context.Background(), "upload.begin", p)
	if err != nil {
		t.Fatal(err)
	}
	id := result.(map[string]any)["id"].(string)
	chunk, _ := json.Marshal(map[string]any{"id": id, "offset": 0, "data": base64.StdEncoding.EncodeToString([]byte("abc"))})
	if _, err = a.handleUpload(context.Background(), "upload.chunk", chunk); err != nil {
		t.Fatal(err)
	}
	if _, err = a.handleUpload(context.Background(), "upload.chunk", chunk); err == nil {
		t.Fatal("replayed chunk accepted")
	}
	finish, _ := json.Marshal(map[string]any{"id": id})
	result, err = a.handleUpload(context.Background(), "upload.finish", finish)
	if err != nil {
		t.Fatal(err)
	}
	if job := waitTestJob(t, a, result); job.Status != "error" || !strings.Contains(job.Message, "SHA-256") {
		t.Fatal(job)
	}
}

func TestBackupRoundTripDoesNotReplaceKernelOrStartProxy(t *testing.T) {
	a := testApp(t)
	p, err := a.saveProfile(context.Background(), Profile{Name: "backup-source", UpdateHours: 24}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "backup.zip")
	if err = a.createBackup(backup); err != nil {
		t.Fatal(err)
	}
	if _, err = a.saveProfile(context.Background(), Profile{ID: p.ID, Name: "edited"}, strings.ReplaceAll(testYAML, "alpha", "beta")); err != nil {
		t.Fatal(err)
	}
	if err = a.restoreBackup(backup); err != nil {
		t.Fatal(err)
	}
	s := a.snapshot()
	if s.Profiles[0].Name != "backup-source" || s.Enabled {
		t.Fatal("backup did not restore safely")
	}
	b, _ := os.ReadFile(filepath.Join(a.root, "profiles", p.ID+".yaml"))
	if string(b) != testYAML {
		t.Fatal("wrong restored profile")
	}
}

func TestBackupRejectsTraversalBeforeChangingState(t *testing.T) {
	a := testApp(t)
	path := filepath.Join(t.TempDir(), "bad.zip")
	f, _ := os.Create(path)
	zw := zip.NewWriter(f)
	entry, _ := zw.Create("../state.json")
	entry.Write([]byte(`{}`))
	zw.Close()
	f.Close()
	before := a.snapshot()
	if err := a.restoreBackup(path); err == nil {
		t.Fatal("accepted zip traversal")
	}
	after := a.snapshot()
	if before.Enabled != after.Enabled || before.ActiveProfile != after.ActiveProfile {
		t.Fatal("changed state before validating archive")
	}
}

func TestRestoreRollsBackFilesWhenStateCommitFails(t *testing.T) {
	a := testApp(t)
	p, err := a.saveProfile(context.Background(), Profile{Name: "old"}, testYAML)
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.zip")
	if err = a.createBackup(backup); err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(testYAML, "alpha", "changed")
	if _, err = a.saveProfile(context.Background(), Profile{ID: p.ID, Name: "current"}, changed); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(a.root, "state.json")
	if err = os.Rename(statePath, statePath+".saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(statePath, 0700); err != nil {
		t.Fatal(err)
	}
	if err = a.restoreBackup(backup); err == nil {
		t.Fatal("expected state commit failure")
	}
	b, _ := os.ReadFile(filepath.Join(a.root, "profiles", p.ID+".yaml"))
	if string(b) != changed || a.snapshot().Profiles[0].Name != "current" {
		t.Fatal("partial restore corrupted current profile")
	}
}

func TestStableReleaseRequiresOfficialMatchingAssetAndHash(t *testing.T) {
	valid := fmt.Sprintf(`{"tag_name":"v1.19.33","published_at":"2026-10-21","assets":[{"name":"mihomo-linux-arm64-v1.19.33.gz","browser_download_url":"https://github.com/MetaCubeX/mihomo/releases/download/v1.19.33/mihomo-linux-arm64-v1.19.33.gz","digest":"sha256:%s","size":123}]}`, strings.Repeat("a", 64))
	if _, err := parseRelease([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{strings.ReplaceAll(valid, "github.com/MetaCubeX", "evil.invalid/MetaCubeX"), strings.ReplaceAll(valid, "sha256:", "md5:"), strings.Replace(valid, `"tag_name"`, `"prerelease":true,"tag_name"`, 1)} {
		if _, err := parseRelease([]byte(bad)); err == nil {
			t.Fatal("accepted unsafe release metadata")
		}
	}
}

func TestKernelVersionRequiresMihomoAndStableSemver(t *testing.T) {
	if got, err := parseKernelVersion("Mihomo Meta v1.19.33 linux arm64"); err != nil || got != "v1.19.33" {
		t.Fatal(got, err)
	}
	if _, err := parseKernelVersion("unknown program v1.19.33"); err == nil {
		t.Fatal("accepted non-mihomo binary")
	}
	for _, text := range []string{"Mihomo Meta v1.19.33-rc1 linux arm64", "Mihomo Meta alpha-v1.19.33 linux arm64"} {
		if _, err := parseKernelVersion(text); err == nil {
			t.Fatal("accepted prerelease", text)
		}
	}
}

func TestFirewallOwnsOnlyNamedChainsAndOrdersDNSBeforeLANBypass(t *testing.T) {
	s := defaults()
	s.Devices = []DevicePolicy{{IP: "192.168.50.10", Mode: "direct"}}
	plan := buildNetworkPlan(s, false, false)
	joined := []string{}
	for _, c := range plan {
		line := c.Program + " " + strings.Join(c.Args, " ")
		joined = append(joined, line)
		if strings.Contains(line, " -F ") || strings.Contains(line, " flush ") {
			t.Fatal("new plan flushes unrelated rules")
		}
	}
	text := strings.Join(joined, "\n")
	direct := strings.Index(text, "-A CWCLASH_NAT -s 192.168.50.10 -j RETURN")
	dns := strings.Index(text, "-A CWCLASH_NAT -p tcp --dport 53")
	lan := strings.Index(text, "-A CWCLASH_NAT -d 192.168.0.0/16")
	if direct < 0 || !(direct < dns && dns < lan) {
		t.Fatal("DNS/direct policy order is wrong")
	}
	if !strings.Contains(text, "--tproxy-mark 0x910000/0xff0000") {
		t.Fatal("missing scoped route mark")
	}
}

func TestIPv6FallbackBlocksForwardingButKeepsLAN(t *testing.T) {
	s := defaults()
	plan := buildNetworkPlan(s, true, false)
	var text strings.Builder
	for _, c := range plan {
		text.WriteString(c.Program + " " + strings.Join(c.Args, " ") + "\n")
	}
	v := text.String()
	if !strings.Contains(v, "CWCLASH6_FWD -d fe80::/10 -j RETURN") || !strings.Contains(v, "CWCLASH6_FWD -j REJECT") {
		t.Fatal("IPv6 fallback does not protect clients")
	}
	if !strings.Contains(v, "-I FORWARD 1 -i br0 -j CWCLASH6_FWD") {
		t.Fatal("IPv6 guard may be skipped behind a firmware ACCEPT rule")
	}
	if strings.Contains(v, "ip -6 rule add") {
		t.Fatal("fallback unexpectedly takes over IPv6 routing")
	}
}

func TestDirectModeDoesNotCaptureTraffic(t *testing.T) {
	s := defaults()
	s.Settings.Mode = "direct"
	for _, c := range buildNetworkPlan(s, true, true) {
		line := strings.Join(c.Args, " ")
		if strings.Contains(line, "PREROUTING") || c.Program == "ip" {
			t.Fatal("direct mode captures traffic")
		}
	}
}

func TestQUICBlockCoversTPROXYInputAndForwardedTraffic(t *testing.T) {
	s := defaults()
	s.Settings.BlockQUIC = true
	s.Devices = []DevicePolicy{{IP: "192.168.50.10", Mode: "direct"}, {IP: "2001:db8::10", Mode: "direct"}}
	var lines []string
	for _, command := range buildNetworkPlan(s, true, true) {
		lines = append(lines, command.Program+" "+strings.Join(command.Args, " "))
	}
	text := strings.Join(lines, "\n")
	for _, program := range []string{"iptables", "ip6tables"} {
		for _, chain := range []string{"INPUT", "FORWARD"} {
			if !strings.Contains(text, program+" -t filter -I "+chain+" 1 -i br0 -j CWCLASH_QUIC") {
				t.Fatal("QUIC bypasses " + program + " " + chain)
			}
		}
	}
	if strings.Contains(text, "-I OUTPUT") {
		t.Fatal("QUIC block must not block the core's own QUIC transport")
	}
}

func TestPolicyOwnershipDoesNotMatchOtherMarksOrTables(t *testing.T) {
	for _, text := range []string{"20210: from all fwmark 0x910000/0xff0000 lookup 202", "20210: from all fwmark 9502720/16711680 table 202"} {
		if !ownedPolicyRulePresent(text) {
			t.Fatal("owned rule not recognized", text)
		}
	}
	for _, text := range []string{"20210: from all fwmark 0x920000/0xff0000 lookup 202", "20211: from all fwmark 0x910000/0xff0000 lookup 202", "20210: from all fwmark 0x910000/0xff0000 lookup 203", "20210: from all lookup 202"} {
		if ownedPolicyRulePresent(text) {
			t.Fatal("matched foreign rule", text)
		}
	}
	if !isOwnedLocalRoute("local default dev lo scope host") || isOwnedLocalRoute("default via 192.168.50.1 dev br0") || isOwnedLocalRoute("local 10.0.0.0/8 dev lo") {
		t.Fatal("route ownership match is too broad")
	}
}

func TestDNSParsingAndFailureResponse(t *testing.T) {
	query := []byte{0, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0, 0, 1, 0, 1}
	name, typ := dnsQuestion(query)
	if name != "example.com" || typ != 1 {
		t.Fatal(name, typ)
	}
	response := dnsFailure(query)
	if response[0] != 0 || response[1] != 1 || response[2]&0x80 == 0 || response[3]&15 != 2 {
		t.Fatal("invalid DNS failure response")
	}
	if !bytes.Equal(response[12:], query[12:]) {
		t.Fatal("lost question")
	}
}

func TestRedactionRemovesAccessTokensAndPasswords(t *testing.T) {
	input := "request https://name:pass@example.com/subscribe?token=abc https://example.com/private-token-in-path password=hello secret:world"
	out := redact(input)
	for _, secret := range []string{"name:pass", "token=abc", "password=hello", "secret:world", "private-token-in-path"} {
		if strings.Contains(out, secret) {
			t.Fatal(out)
		}
	}
}
