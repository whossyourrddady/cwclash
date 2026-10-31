// SPDX-License-Identifier: GPL-3.0-or-later
package app

import "gopkg.in/yaml.v3"

func previewConfig(raw []byte) ([]byte, error) {
	doc, err := parseYAML(raw)
	if err != nil {
		return nil, err
	}
	doc["allow-lan"] = false
	doc["bind-address"] = "127.0.0.1"
	doc["redir-port"] = 0
	doc["tproxy-port"] = 0
	doc["port"] = 0
	doc["socks-port"] = 0
	doc["listeners"] = []any{}
	doc["tunnels"] = []any{}
	doc["tun"] = map[string]any{"enable": false}
	doc["routing-mark"] = 0
	doc["authentication"] = []any{}
	dns := asMap(doc["dns"])
	dns["enable"] = false
	dns["listen"] = ""
	doc["dns"] = dns
	return yaml.Marshal(doc)
}
