// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStoppedGroupsPreviewWithoutStartingCore(t *testing.T) {
	a := testApp(t)
	p, err := a.saveProfile(context.Background(), Profile{Name: "offline"}, testYAML+"proxy-providers:\n  remote:\n    type: http\n    url: https://secret.invalid/token\n")
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.state.ActiveProfile = p.ID
	a.state.Settings.OverrideYAML = "proxies:\n  - {name: overridden, type: ss, server: secret.invalid, password: secret-password}\nproxy-groups:\n  - {name: Route, type: select, proxies: [overridden, DIRECT], use: [remote]}\n"
	a.mu.Unlock()
	v, err := a.dispatch(context.Background(), "groups", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	text := string(b)
	for _, want := range []string{`"source":"config"`, `"overridden"`, `"all":["overridden","DIRECT"]`, `"providers":["remote"]`} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s in %s", want, text)
		}
	}
	for _, secret := range []string{"secret.invalid", "secret-password", "alpha", `"now"`, `"history"`} {
		if strings.Contains(text, secret) {
			t.Fatalf("unexpected field %s", secret)
		}
	}
	if a.isRunning() {
		t.Fatal("preview started core")
	}
}

func TestProviderPreviewReadsLocalCacheWithoutLeakingSecrets(t *testing.T) {
	a := testApp(t)
	raw := testYAML + `proxy-providers:
  remote:
    type: http
    url: https://example.invalid/private-token
  embedded:
    type: inline
    payload:
      - {name: inline-node, type: ss, server: private-server, password: private-password}
  local:
    type: file
    path: local.yaml
`
	if _, err := a.saveProfile(context.Background(), Profile{Name: "preview"}, raw); err != nil {
		t.Fatal(err)
	}
	doc, _ := parseYAML([]byte(raw))
	if err := a.normalizeProviderPaths(doc); err != nil {
		t.Fatal(err)
	}
	providers := asMap(doc["proxy-providers"])
	for _, name := range []string{"remote", "local"} {
		path := asMap(providers[name])["path"].(string)
		if err := atomicWrite(path, []byte("proxies:\n  - {name: "+name+"-node, type: ss, server: private-server, password: private-password}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	v, err := a.previewGroups()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(v)
	for _, want := range []string{"inline-node", "remote-node", "local-node", "本地缓存", "本地文件", "配置内嵌"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, secret := range []string{"private-server", "private-password", "private-token"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("secret leaked", secret)
		}
	}
	// A same-named provider with a changed source must not display old cached nodes.
	changed, _ := parseYAML([]byte(strings.ReplaceAll(raw, "private-token", "different-token")))
	if err := a.normalizeProviderPaths(changed); err != nil {
		t.Fatal(err)
	}
	oldPath := asMap(providers["remote"])["path"]
	newPath := asMap(asMap(changed["proxy-providers"])["remote"])["path"]
	if oldPath == newPath {
		t.Fatal("sources share cache")
	}
	fresh, _ := parseYAML([]byte(strings.ReplaceAll(raw, "private-token", "different-token")))
	preview := a.previewProviders(fresh, []string{"remote"})
	if len(preview[0].Nodes) != 0 {
		t.Fatal("stale other-source cache used")
	}
	// Normalization is stable and paths do not feed their own cache identity.
	delete(asMap(changed["proxy-providers"]), "local")
	encoded, _ := yaml.Marshal(changed)
	again, _ := parseYAML(encoded)
	if err := a.normalizeProviderPaths(again); err != nil {
		t.Fatal(err)
	}
	if asMap(asMap(again["proxy-providers"])["remote"])["path"] != newPath {
		t.Fatal("unstable cache identity")
	}
}

func TestPreviewFileBounds(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "cache.yaml")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPreviewFile(base, path, 4); err == nil {
		t.Fatal("accepted oversized file")
	}
	outside := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPreviewFile(base, outside, 100); err == nil {
		t.Fatal("accepted outside file")
	}
}

func TestStoppedGroupsEmptyAndUngrouped(t *testing.T) {
	a := testApp(t)
	v, err := a.previewGroups()
	if err != nil || len(v.(map[string]any)["proxies"].(map[string]any)) != 0 {
		t.Fatalf("empty preview: %v %v", v, err)
	}
	p, err := a.saveProfile(context.Background(), Profile{Name: "nodes"}, "proxies:\n  - {name: alone, type: socks5, server: 127.0.0.1, port: 1080, udp: true}\n")
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.state.ActiveProfile = p.ID
	a.mu.Unlock()
	v, err = a.previewGroups()
	if err != nil {
		t.Fatal(err)
	}
	nodes := v.(map[string]any)["proxies"].(map[string]any)
	if len(nodes) != 1 || nodes["alone"].(map[string]any)["udp"] != true {
		t.Fatalf("lost standalone node: %v", nodes)
	}
}

func TestStoppedGroupsRejectInvalidProfileAndOversize(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	a.state.ActiveProfile = "../secret"
	a.mu.Unlock()
	if _, err := a.previewGroups(); err == nil {
		t.Fatal("accepted traversal")
	}
	a.mu.Lock()
	a.state.ActiveProfile = "abcdefgh"
	a.mu.Unlock()
	if err := os.WriteFile(filepath.Join(a.root, "profiles", "abcdefgh.yaml"), []byte("proxies: []\n#"+strings.Repeat("x", MaxConfigSize)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.previewGroups(); err == nil {
		t.Fatal("accepted oversized config")
	}
}
