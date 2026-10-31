// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	result["providerPreviews"] = a.previewProviders(doc, providers)
	return result, nil
}

type previewNode struct {
	Name string `json:"name"`
	Type string `json:"type"`
	UDP  bool   `json:"udp"`
}

type providerPreview struct {
	Name      string        `json:"name"`
	Source    string        `json:"source"`
	UpdatedAt string        `json:"updatedAt,omitempty"`
	Nodes     []previewNode `json:"nodes"`
}

// Read only local, bounded data. Never download or imitate runtime group filters.
func (a *App) previewProviders(doc map[string]any, names []string) []providerPreview {
	out := []providerPreview{}
	if a.normalizeProviderPaths(doc) != nil {
		return out
	}
	budget := int64(MaxConfigSize)
	for i, name := range names {
		item := providerPreview{Name: name, Source: "尚无缓存", Nodes: []previewNode{}}
		p := asMap(asMap(doc["proxy-providers"])[name])
		payload := asList(p["payload"])
		if p["type"] == "inline" {
			item.Source = "配置内嵌"
		} else if i >= 128 || budget <= 0 {
			item.Source = "超出预览上限"
		} else {
			path, _ := p["path"].(string)
			base := a.coreDataDir()
			if p["type"] == "file" {
				base = filepath.Join(a.root, "assets")
			}
			data, stamp, err := readPreviewFile(base, path, budget)
			budget -= int64(len(data))
			if err == nil {
				var parsed map[string]any
				if yaml.Unmarshal(data, &parsed) == nil {
					payload = asList(parsed["proxies"])
					item.Source = "本地缓存"
					if p["type"] == "file" {
						item.Source = "本地文件"
					}
					item.UpdatedAt = stamp
				} else {
					item.Source = "缓存格式无法预览"
				}
			}
		}
		for _, v := range payload {
			n := asMap(v)
			name, _ := n["name"].(string)
			typ, _ := n["type"].(string)
			udp, _ := n["udp"].(bool)
			if name != "" {
				item.Nodes = append(item.Nodes, previewNode{name, typ, udp})
			}
		}
		out = append(out, item)
	}
	return out
}

func readPreviewFile(base, path string, limit int64) ([]byte, string, error) {
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		return nil, "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, "", err
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return nil, "", fmt.Errorf("缓存路径越界")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("缓存文件不可读")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return data, "", fmt.Errorf("缓存超过预览上限")
	}
	return data, info.ModTime().UTC().Format("2006-01-02T15:04:05Z"), nil
}
