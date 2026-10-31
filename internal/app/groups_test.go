// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
