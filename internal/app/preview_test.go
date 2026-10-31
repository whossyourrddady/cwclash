// SPDX-License-Identifier: GPL-3.0-or-later
package app

import "testing"

func TestPreviewConfigNeverExposesTrafficListeners(t *testing.T) {
	a := testApp(t)
	s := a.snapshot()
	raw, err := a.compileConfig(s, []byte(testYAML), s.Settings.ControllerPort)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\ntunnels: [tcp,0.0.0.0:9999,example.test:443,DIRECT]\n")...)
	out, err := previewConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseYAML(out)
	if err != nil {
		t.Fatal(err)
	}
	if doc["allow-lan"] != false || doc["bind-address"] != "127.0.0.1" || doc["redir-port"] != 0 || doc["tproxy-port"] != 0 || doc["routing-mark"] != 0 || asMap(doc["dns"])["enable"] != false || asMap(doc["tun"])["enable"] != false || len(asList(doc["listeners"])) != 0 || len(asList(doc["tunnels"])) != 0 {
		t.Fatal("preview exposed traffic interception", doc)
	}
	a.dev = false
	a.previewMode = true
	if err = a.applyNetwork(s); err != nil {
		t.Fatal("preview tried to install routing rules", err)
	}
}
