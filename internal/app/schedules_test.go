// SPDX-License-Identifier: GPL-3.0-or-later
package app

import "testing"

func TestResourceSchedulesPreserveDefaultsAndCacheIdentity(t *testing.T) {
	a := testApp(t)
	s := a.snapshot()
	raw := []byte(`geo-auto-update: true
geo-update-interval: 48
proxy-providers:
  remote:
    type: http
    url: https://example.test/proxies.yaml
    interval: 500
rule-providers:
  local:
    type: file
    path: rules.txt
    interval: 700
  remote:
    type: http
    url: https://example.test/rules.yaml
    interval: 800
rules: [MATCH,DIRECT]
`)
	compile := func() map[string]any {
		t.Helper()
		data, err := a.compileConfig(s, raw, 19090)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := parseYAML(data)
		if err != nil {
			t.Fatal(err)
		}
		return doc
	}
	base := compile()
	if base["geo-auto-update"] != true || base["geo-update-interval"] != 48 || asMap(asMap(base["proxy-providers"])["remote"])["interval"] != 500 {
		t.Fatal("default schedule changed")
	}
	no := false
	s.Settings.GeoAutoUpdate = &no
	s.Settings.GeoUpdateHours = 12
	s.Settings.ProviderUpdateHours = 2
	next := compile()
	if next["geo-auto-update"] != false || next["geo-update-interval"] != 12 {
		t.Fatal("GEO override ignored")
	}
	for _, kind := range []string{"proxy-providers", "rule-providers"} {
		old := asMap(asMap(base[kind])["remote"])
		p := asMap(asMap(next[kind])["remote"])
		if p["interval"] != 7200 || p["path"] != old["path"] {
			t.Fatalf("schedule/cache invalid for %s: %v", kind, p)
		}
	}
	if asMap(asMap(next["rule-providers"])["local"])["interval"] != 700 {
		t.Fatal("file provider changed")
	}
	for _, n := range []int{-1, 721} {
		s.Settings.ProviderUpdateHours = n
		if validateSettings(s.Settings) == nil {
			t.Fatal("invalid interval accepted")
		}
	}
}
