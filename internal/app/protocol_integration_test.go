// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type fixtureLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *fixtureLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(b)
}
func (l *fixtureLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
func freeUDPPort(t *testing.T) int {
	t.Helper()
	l, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	port := l.LocalAddr().(*net.UDPAddr).Port
	l.Close()
	return port
}

func startFixtureCore(t *testing.T, binary string, config map[string]any, port int, secret string) *fixtureLog {
	t.Helper()
	dir := t.TempDir()
	b, err := yaml.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.yaml")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	log := &fixtureLog{}
	cmd := exec.Command(binary, "-d", dir, "-f", path)
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-done })
	client := &http.Client{Timeout: 300 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			t.Fatalf("fixture core exited: %v\n%s", err, log.String())
		default:
		}
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/version", port), nil)
		req.Header.Set("Authorization", "Bearer "+secret)
		res, err := client.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 {
				return log
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("fixture core not ready: %s", log.String())
	return log
}

func fixtureCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "cwclash loopback fixture"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	cert, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func TestNativeProtocolLoopback(t *testing.T) {
	binary := os.Getenv("CWCLASH_TEST_CORE_PATH")
	if binary == "" {
		t.Skip("set CWCLASH_TEST_CORE_PATH to run actual mihomo protocol tests")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	cert, key := fixtureCertificate(t)
	uuid := "d0ac24f4-7436-4f04-8f6a-e01209c392ba"
	password := "cwclash-loopback-only"
	serverPort := freeTCPPort(t)
	serverSecret := randomID(20)
	type protocol struct {
		name     string
		listener map[string]any
		node     map[string]any
	}
	protocols := []protocol{}
	for _, name := range []string{"ss", "vmess", "vless", "trojan", "hysteria2", "tuic"} {
		port := freeTCPPort(t)
		if name == "hysteria2" || name == "tuic" {
			port = freeUDPPort(t)
		}
		listener := map[string]any{"name": "fixture-" + name, "type": name, "listen": "127.0.0.1", "port": port}
		node := map[string]any{"name": name, "type": name, "server": "127.0.0.1", "port": port, "udp": true}
		switch name {
		case "ss":
			listener["type"] = "shadowsocks"
			listener["cipher"] = "aes-128-gcm"
			listener["password"] = password
			node["cipher"] = "aes-128-gcm"
			node["password"] = password
		case "vmess":
			listener["users"] = []any{map[string]any{"username": "fixture", "uuid": uuid, "alterId": 0}}
			node["uuid"] = uuid
			node["alterId"] = 0
			node["cipher"] = "auto"
		case "vless":
			listener["users"] = []any{map[string]any{"username": "fixture", "uuid": uuid}}
			listener["certificate"] = cert
			listener["private-key"] = key
			node["uuid"] = uuid
			node["tls"] = true
			node["servername"] = "localhost"
			node["skip-cert-verify"] = true
		case "trojan":
			listener["users"] = []any{map[string]any{"username": "fixture", "password": password}}
			listener["certificate"] = cert
			listener["private-key"] = key
			node["password"] = password
			node["sni"] = "localhost"
			node["skip-cert-verify"] = true
		case "hysteria2":
			listener["users"] = map[string]string{"fixture": password}
			listener["certificate"] = cert
			listener["private-key"] = key
			node["password"] = password
			node["sni"] = "localhost"
			node["skip-cert-verify"] = true
		case "tuic":
			listener["users"] = map[string]string{uuid: password}
			listener["certificate"] = cert
			listener["private-key"] = key
			listener["alpn"] = []string{"h3"}
			node["uuid"] = uuid
			node["password"] = password
			node["sni"] = "localhost"
			node["alpn"] = []string{"h3"}
			node["skip-cert-verify"] = true
			node["udp-relay-mode"] = "native"
		}
		protocols = append(protocols, protocol{name, listener, node})
	}
	listeners := []any{}
	nodes := []any{}
	names := []string{}
	for _, p := range protocols {
		listeners = append(listeners, p.listener)
		nodes = append(nodes, p.node)
		names = append(names, p.name)
	}
	serverLog := startFixtureCore(t, binary, map[string]any{"log-level": "warning", "external-controller": fmt.Sprintf("127.0.0.1:%d", serverPort), "secret": serverSecret, "listeners": listeners, "rules": []string{"MATCH,DIRECT"}, "dns": map[string]any{"enable": false}}, serverPort, serverSecret)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "cwclash-protocol-tcp-ok") }))
	defer echo.Close()
	udpEcho, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer udpEcho.Close()
	go func() {
		b := make([]byte, 2048)
		for {
			n, peer, err := udpEcho.ReadFromUDP(b)
			if err != nil {
				return
			}
			_, _ = udpEcho.WriteToUDP(b[:n], peer)
		}
	}()
	a := testApp(t)
	coreBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	sha := hashBytes(coreBytes)
	k, err := a.storePackedCore(binary, sha)
	if err != nil {
		t.Fatal(err)
	}
	k.Version = "native-test"
	s := a.snapshot()
	s.Kernel = k
	s.Settings.MixedPort = freeTCPPort(t)
	s.Settings.ControllerPort = freeTCPPort(t)
	s.Settings.DNSPort = freeTCPPort(t)
	s.Settings.LogLevel = "debug"
	if err = a.persist(s); err != nil {
		t.Fatal(err)
	}
	profile, err := yaml.Marshal(map[string]any{"proxies": nodes, "proxy-groups": []any{map[string]any{"name": "Test", "type": "select", "proxies": names}}, "rules": []string{"MATCH,Test"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.saveProfile(context.Background(), Profile{Name: "native-loopback"}, string(profile)); err != nil {
		t.Fatal(err)
	}
	if err = a.startService(false); err != nil {
		t.Fatalf("client start: %v", err)
	}
	defer a.stopService(false)
	proxyURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", s.Settings.MixedPort))
	t.Run("profile-save-restart-boundary", func(t *testing.T) {
		original := a.core
		current := *findProfile(a.snapshot(), a.snapshot().ActiveProfile)
		if _, err := a.saveProfile(context.Background(), Profile{Name: "inactive-fixture"}, string(profile)); err != nil {
			t.Fatal(err)
		}
		if a.core != original || !a.isRunning() {
			t.Fatal("inactive save restarted actual core")
		}
		if _, err := a.saveProfile(context.Background(), current, string(profile)); err != nil {
			t.Fatal(err)
		}
		if a.core != original || !a.isRunning() {
			t.Fatal("unchanged active save restarted actual core")
		}
		if _, err := a.saveProfile(context.Background(), current, string(profile)+"\n# changed active config\n"); err != nil {
			t.Fatal(err)
		}
		if a.core == original || !a.isRunning() {
			t.Fatal("changed active profile did not restart core")
		}
	})
	type result struct {
		Protocol string `json:"protocol"`
		TCP      bool   `json:"tcp"`
		UDP      bool   `json:"udp"`
		Error    string `json:"error,omitempty"`
	}
	report := []result{}
	for _, p := range protocols {
		r := result{Protocol: p.name}
		t.Run(p.name, func(t *testing.T) {
			if _, err := a.controller(context.Background(), "PUT", "/proxies/Test", nil, map[string]string{"name": p.name}); err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 8 * time.Second}
			response, err := client.Get(echo.URL)
			if err != nil {
				r.Error = err.Error()
				t.Errorf("TCP failed: %v\nserver: %s", err, serverLog.String())
				return
			}
			b, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(b) != "cwclash-protocol-tcp-ok" {
				r.Error = fmt.Sprintf("HTTP %d %s", response.StatusCode, b)
				a.mu.RLock()
				start := len(a.logs) - 12
				if start < 0 {
					start = 0
				}
				clientLogs := append([]LogEntry(nil), a.logs[start:]...)
				a.mu.RUnlock()
				t.Errorf("TCP failed: %s\nserver: %s\nclient: %+v", r.Error, serverLog.String(), clientLogs)
				return
			}
			r.TCP = true
			if err = socksUDPExchange(s.Settings.MixedPort, udpEcho.LocalAddr().(*net.UDPAddr).Port); err != nil {
				r.Error = err.Error()
				t.Errorf("UDP failed: %v\nserver: %s", err, serverLog.String())
				return
			}
			r.UDP = true
		})
		report = append(report, r)
	}
	t.Run("selection-restored-without-cache", func(t *testing.T) {
		if err := a.selectGroup(context.Background(), "Test", "trojan"); err != nil {
			t.Fatal(err)
		}
		if err := a.stopService(false); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(a.coreDataDir(), "cache.db")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := a.startService(false); err != nil {
			t.Fatal(err)
		}
		v, err := a.controller(context.Background(), "GET", "/proxies/Test", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if v.(map[string]any)["now"] != "trojan" {
			t.Fatal("manual group selection was lost when RAM cache was removed")
		}
	})
	if reportPath := os.Getenv("CWCLASH_TEST_PROTOCOL_REPORT"); reportPath != "" {
		b, _ := json.MarshalIndent(map[string]any{"time": time.Now().Format(time.RFC3339), "scope": "Native mihomo on host, localhost protocol server/client, no router transparent proxy", "results": report}, "", "  ")
		_ = os.MkdirAll(filepath.Dir(reportPath), 0700)
		_ = os.WriteFile(reportPath, b, 0600)
	}
	t.Run("file-provider-backup-restore", func(t *testing.T) {
		asset, err := yaml.Marshal(map[string]any{"proxies": []any{protocols[0].node}})
		if err != nil {
			t.Fatal(err)
		}
		assetPath := filepath.Join(a.root, "assets", "local.yaml")
		if err = atomicWrite(assetPath, asset, 0600); err != nil {
			t.Fatal(err)
		}
		raw := "proxy-providers:\n  file:\n    type: file\n    path: local.yaml\nproxy-groups:\n  - name: LocalFile\n    type: select\n    use: [file]\nrules:\n  - MATCH,LocalFile\n"
		p, err := a.saveProfile(context.Background(), Profile{Name: "local-file-provider"}, raw)
		if err != nil {
			t.Fatal(err)
		}
		next := a.snapshot()
		next.ActiveProfile = p.ID
		if err = a.applyState(next); err != nil {
			t.Fatal(err)
		}
		checkHTTP := func() {
			t.Helper()
			tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			response, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(echo.URL)
			if err != nil {
				t.Fatal(err)
			}
			b, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(b) != "cwclash-protocol-tcp-ok" {
				t.Fatalf("file provider did not forward: HTTP %d", response.StatusCode)
			}
		}
		checkHTTP()
		backup := filepath.Join(a.run, "file-provider-backup.zip")
		if err = a.createBackup(backup); err != nil {
			t.Fatal(err)
		}
		if err = a.stopService(false); err != nil {
			t.Fatal(err)
		}
		if err = atomicWrite(assetPath, []byte("proxies: [broken"), 0600); err != nil {
			t.Fatal(err)
		}
		if err = a.restoreBackup(backup); err != nil {
			t.Fatal(err)
		}
		if a.isRunning() {
			t.Fatal("restore must remain stopped for review")
		}
		restored, err := os.ReadFile(assetPath)
		if err != nil || !bytes.Equal(restored, asset) {
			t.Fatal("incoming file-provider asset was not restored")
		}
		if err = a.startService(false); err != nil {
			t.Fatal(err)
		}
		checkHTTP()
	})
	t.Run("http-provider-cache-stays-in-ram-during-restore", func(t *testing.T) {
		asset, err := yaml.Marshal(map[string]any{"proxies": []any{protocols[0].node}})
		if err != nil {
			t.Fatal(err)
		}
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(asset) }))
		defer provider.Close()
		raw := fmt.Sprintf("proxy-providers:\n  fixture-http:\n    type: http\n    url: %q\n    interval: 3600\nproxy-groups:\n  - name: LocalHTTP\n    type: select\n    use: [fixture-http]\nrules:\n  - MATCH,LocalHTTP\n", provider.URL)
		p, err := a.saveProfile(context.Background(), Profile{Name: "http-provider"}, raw)
		if err != nil {
			t.Fatal(err)
		}
		next := a.snapshot()
		next.ActiveProfile = p.ID
		if err = a.applyState(next); err != nil {
			t.Fatal(err)
		}
		compiled, err := a.compileConfig(next, []byte(raw), next.Settings.ControllerPort)
		if err != nil {
			t.Fatal(err)
		}
		doc := decodeMap(t, compiled)
		providers := asMap(doc["proxy-providers"])
		cache := asMap(providers["fixture-http"])["path"].(string)
		if !strings.HasPrefix(cache, a.coreDataDir()+string(os.PathSeparator)) {
			t.Fatal("HTTP provider cache escaped RAM data directory")
		}
		backup := filepath.Join(a.run, "http-provider-backup.zip")
		if err = a.createBackup(backup); err != nil {
			t.Fatal(err)
		}
		if err = a.stopService(false); err != nil {
			t.Fatal(err)
		}
		canary := []byte("live RAM cache must not be overwritten during backup validation")
		if err = atomicWrite(cache, canary, 0600); err != nil {
			t.Fatal(err)
		}
		if err = a.restoreBackup(backup); err != nil {
			t.Fatal(err)
		}
		actual, _ := os.ReadFile(cache)
		if !bytes.Equal(actual, canary) {
			t.Fatal("restore validation changed the live RAM provider cache")
		}
		if err = os.Remove(cache); err != nil {
			t.Fatal(err)
		}
		if err = a.startService(false); err != nil {
			t.Fatal(err)
		}
		tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		response, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(echo.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "cwclash-protocol-tcp-ok" {
			t.Fatal("HTTP provider did not forward after cache regeneration")
		}
	})
	t.Run("usb-layout-raw-core-and-persistent-provider-cache", func(t *testing.T) {
		if err := a.stopService(false); err != nil {
			t.Fatal(err)
		}
		usb, err := NewWithStorage(a.root, filepath.Join(t.TempDir(), "usb-runtime"), "127.0.0.1:0", "usb-layout-test", true, StorageUSB)
		if err != nil {
			t.Fatal(err)
		}
		rawKernel, err := usb.storeCore(binary, sha)
		if err != nil {
			t.Fatal(err)
		}
		rawKernel.Version = k.Version
		next := usb.snapshot()
		next.Kernel = rawKernel
		if err = usb.persist(next); err != nil {
			t.Fatal(err)
		}
		asset, _ := yaml.Marshal(map[string]any{"proxies": []any{protocols[0].node}})
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(asset) }))
		defer provider.Close()
		raw := fmt.Sprintf("proxy-providers:\n  usb-http:\n    type: http\n    url: %q\n    interval: 3600\nproxy-groups:\n  - name: USBHTTP\n    type: select\n    use: [usb-http]\nrules:\n  - MATCH,USBHTTP\n", provider.URL)
		p, err := usb.saveProfile(context.Background(), Profile{Name: "usb-cache-fixture"}, raw)
		if err != nil {
			t.Fatal(err)
		}
		next = usb.snapshot()
		next.ActiveProfile = p.ID
		if err = usb.applyState(next); err != nil {
			t.Fatal(err)
		}
		if err = usb.startService(false); err != nil {
			t.Fatal(err)
		}
		compiled, err := usb.compileConfig(next, []byte(raw), next.Settings.ControllerPort)
		if err != nil {
			t.Fatal(err)
		}
		doc := decodeMap(t, compiled)
		cache := asMap(asMap(doc["proxy-providers"])["usb-http"])["path"].(string)
		if !strings.HasPrefix(cache, filepath.Join(usb.root, "data")+string(os.PathSeparator)) {
			t.Fatal("USB provider cache did not use persistent data directory")
		}
		if _, err = os.Stat(cache); err != nil {
			t.Fatal(err)
		}
		if err = usb.stopService(false); err != nil {
			t.Fatal(err)
		}
		preview, err := usb.previewGroups()
		if err != nil {
			t.Fatal(err)
		}
		cached := preview.(map[string]any)["providerPreviews"].([]providerPreview)
		if len(cached) != 1 || len(cached[0].Nodes) != 1 || cached[0].Source != "本地缓存" {
			t.Fatalf("actual mihomo cache was not visible while stopped: %+v", cached)
		}
		provider.Close()
		fresh, err := NewWithStorage(usb.root, filepath.Join(t.TempDir(), "fresh-runtime"), "127.0.0.1:0", "usb-layout-test", true, StorageUSB)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.stopService(false)
		if err = fresh.startService(false); err != nil {
			t.Fatal(err)
		}
		tr := &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		response, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(echo.URL)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if string(body) != "cwclash-protocol-tcp-ok" {
			t.Fatal("USB cache could not forward with a new runtime and offline provider")
		}
	})
}

func socksUDPExchange(proxyPort, targetPort int) error {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", proxyPort), 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err = conn.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	var hello [2]byte
	if _, err = io.ReadFull(conn, hello[:]); err != nil {
		return err
	}
	if hello != [2]byte{5, 0} {
		return fmt.Errorf("SOCKS5 method rejected")
	}
	if _, err = conn.Write([]byte{5, 3, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var header [4]byte
	if _, err = io.ReadFull(conn, header[:]); err != nil {
		return err
	}
	if header[1] != 0 {
		return fmt.Errorf("UDP associate rejected: %d", header[1])
	}
	var host net.IP
	switch header[3] {
	case 1:
		b := make([]byte, 4)
		if _, err = io.ReadFull(conn, b); err != nil {
			return err
		}
		host = net.IP(b)
	case 4:
		b := make([]byte, 16)
		if _, err = io.ReadFull(conn, b); err != nil {
			return err
		}
		host = net.IP(b)
	default:
		return fmt.Errorf("unexpected SOCKS relay address type %d", header[3])
	}
	var portBytes [2]byte
	if _, err = io.ReadFull(conn, portBytes[:]); err != nil {
		return err
	}
	port := int(binary.BigEndian.Uint16(portBytes[:]))
	if host.IsUnspecified() {
		host = net.IPv4(127, 0, 0, 1)
	}
	udp, err := net.DialUDP("udp", nil, &net.UDPAddr{IP: host, Port: port})
	if err != nil {
		return err
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(8 * time.Second))
	payload := []byte("cwclash-protocol-udp-ok")
	packet := []byte{0, 0, 0, 1, 127, 0, 0, 1, byte(targetPort >> 8), byte(targetPort)}
	packet = append(packet, payload...)
	if _, err = udp.Write(packet); err != nil {
		return err
	}
	response := make([]byte, 4096)
	n, err := udp.Read(response)
	if err != nil {
		return err
	}
	if n < 10 || !strings.HasSuffix(string(response[:n]), string(payload)) {
		return fmt.Errorf("unexpected UDP echo response")
	}
	return nil
}
