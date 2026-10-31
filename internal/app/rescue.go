// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Watchdog is called by firmware cron. It uses only RAM if the manager died.
func Watchdog(run string) error {
	if runtime.GOOS != "linux" || filepath.Clean(run) != DefaultRuntime {
		return fmt.Errorf("守护检查仅用于路由器的专用运行目录")
	}
	// Use the RAM journal and proc mount table even if manager I/O is blocked.
	if b, err := os.ReadFile(filepath.Join(run, "storage-binding.json")); err == nil {
		mounts, mountErr := readStorageMounts()
		if mountErr != nil || verifyRuntimeStorageBinding(b, mounts) != nil {
			return Rescue(run)
		}
	} else if !os.IsNotExist(err) {
		return Rescue(run)
	}
	if b, err := os.ReadFile(filepath.Join(run, "manager.pid")); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil && pid > 1 {
			cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			parts := bytes.Split(cmd, []byte{0})
			if len(parts) > 1 && string(parts[0]) == filepath.Join(run, "cwclash") && string(parts[1]) == "serve" {
				return nil
			}
		}
	}
	return Rescue(run)
}

func verifyRuntimeStorageBinding(data []byte, mounts []storageMount) error {
	if len(data) > 8192 {
		return fmt.Errorf("运行存储记录无效")
	}
	var binding usbStorage
	if err := json.Unmarshal(data, &binding); err != nil {
		return err
	}
	return binding.validate(mounts, binding.Path)
}

// Rescue uses only the RAM-resident executable and ownership journal. It also
// works after USB-backed /jffs becomes unavailable.
func Rescue(run string) error {
	if runtime.GOOS != "linux" || filepath.Clean(run) != DefaultRuntime {
		return fmt.Errorf("恢复命令仅用于路由器的专用运行目录")
	}
	a := &App{run: run}
	if err := a.cleanupNetwork(); err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(run, "core.pid")); err == nil {
		pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err == nil && pid > 1 {
			cmd, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			if strings.HasPrefix(string(cmd), filepath.Join(run, "mihomo-")) {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	}
	_ = os.Remove(filepath.Join(run, "core.pid"))
	return nil
}
