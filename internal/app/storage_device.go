// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

// Some router kernels expose block devices directly below /sys/block. Their
// canonical block path has no USB ancestor: the hardware ancestry is reached
// through the whole disk's device link (a partition has no such link itself).
// Check the dev attribute first so alternate sysfs entry points cannot select
// a different drive merely because its name resembles the mount source.
func verifyUSBDevice(m storageMount, resolve func(string) (string, error), read func(string) ([]byte, error)) error {
	candidates := []string{path.Join("/sys/dev/block", m.Device)}
	if disk := regexp.MustCompile(`^/dev/(sd[a-z]+)([0-9]*)$`).FindStringSubmatch(m.Source); disk != nil {
		name := disk[1] + disk[2]
		candidates = append(candidates, path.Join("/sys/class/block", name))
		legacy := path.Join("/sys/block", disk[1])
		if disk[2] != "" {
			legacy = path.Join(legacy, name)
		}
		candidates = append(candidates, legacy)
	}
	var notes []string
	seen := map[string]bool{}
	for _, candidate := range candidates {
		block, err := resolve(candidate)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: %v", candidate, err))
			continue
		}
		if seen[block] {
			continue
		}
		seen[block] = true
		dev, err := read(path.Join(block, "dev"))
		if err != nil || strings.TrimSpace(string(dev)) != m.Device {
			notes = append(notes, fmt.Sprintf("%s: dev 编号不匹配或不可读", block))
			continue
		}
		if usbTopology(block) {
			return nil
		}
		// Modern layout, legacy whole disk, then legacy partition parent.
		links := []string{path.Join(block, "device")}
		if _, err := read(path.Join(block, "partition")); err == nil {
			links = append(links, path.Join(path.Dir(block), "device"))
		}
		for _, link := range links {
			topology, err := resolve(link)
			if err == nil && usbTopology(topology) {
				return nil
			}
		}
		notes = append(notes, fmt.Sprintf("%s: 设备链接未发现 USB 上级", block))
	}
	return fmt.Errorf("无法核验 /jffs 的 USB 设备 %s（%s），未更改安装：%s", m.Device, m.Source, strings.Join(notes, "; "))
}
