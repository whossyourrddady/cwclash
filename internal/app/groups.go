// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Preview only exposes display fields, never credentials or subscription URLs.
// Dynamic provider/filter membership and runtime selections belong to mihomo.
func (a *App) previewGroups() (any, error) {
	s := a.snapshot()
	proxies := map[string]any{}
	result := map[string]any{"source": "config", "proxies": proxies, "providers": []string{}}
	if s.ActiveProfile == "" {
		return result, nil
	}
	if !validID(s.ActiveProfile) {
		return nil, fmt.Errorf("当前配置标识无效")
	}
	f, err := os.Open(filepath.Join(a.root, "profiles", s.ActiveProfile+".yaml"))
	if err != nil {
		return nil, fmt.Errorf("无法读取当前配置")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxConfigSize+1))
	if err != nil {
		return nil, fmt.Errorf("无法读取当前配置")
	}
	doc, err := parseYAML(raw)
	if err != nil {
		return nil, fmt.Errorf("当前配置无法预览，请检查 YAML 格式")
	}
	if s.Settings.OverrideYAML != "" {
		var override map[string]any
		if err := yaml.Unmarshal([]byte(s.Settings.OverrideYAML), &override); err != nil {
			return nil, fmt.Errorf("覆写 YAML 无法预览，请检查格式")
		}
		mergeMap(doc, override)
	}
	for _, value := range asList(doc["proxies"]) {
		node := asMap(value)
		name, _ := node["name"].(string)
		typ, _ := node["type"].(string)
		udp, _ := node["udp"].(bool)
		if name != "" {
			proxies[name] = map[string]any{"name": name, "type": typ, "udp": udp}
		}
	}
	for _, value := range asList(doc["proxy-groups"]) {
		group := asMap(value)
		name, _ := group["name"].(string)
		typ, _ := group["type"].(string)
		if name == "" {
			continue
		}
		members := []string{}
		seen := map[string]bool{}
		for _, v := range asList(group["proxies"]) {
			if n, ok := v.(string); ok && !seen[n] {
				members = append(members, n)
				seen[n] = true
			}
		}
		proxies[name] = map[string]any{"name": name, "type": typ, "all": members}
	}
	providers := []string{}
	for name := range asMap(doc["proxy-providers"]) {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	result["providers"] = providers
	return result, nil
}
