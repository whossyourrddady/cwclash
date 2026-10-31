// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

const (
	StorageNative = "native"
	StorageUSB    = "usb-jffs"
)

type storageMount struct {
	ID                                   int
	Device, Point, Kind, Options, Source string
}

type usbStorage struct {
	JFFS, Root storageMount
	Path       string
}

func parseStorageMounts(data string) ([]storageMount, error) {
	var mounts []storageMount
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(data, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		separator := -1
		for i := 6; i < len(f); i++ {
			if f[i] == "-" {
				separator = i
				break
			}
		}
		if len(f) < 10 || separator < 6 || separator+3 >= len(f) {
			return nil, fmt.Errorf("无法识别系统挂载信息")
		}
		id, err := strconv.Atoi(f[0])
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("挂载编号无效")
		}
		mounts = append(mounts, storageMount{ID: id, Device: f[2], Point: path.Clean(decode.Replace(f[4])), Kind: f[separator+1], Options: f[5], Source: decode.Replace(f[separator+2])})
	}
	return mounts, nil
}

func mountForPath(mounts []storageMount, path string) (storageMount, bool) {
	var best storageMount
	for _, m := range mounts {
		if path == m.Point || m.Point == "/" || strings.HasPrefix(path, m.Point+"/") {
			if len(m.Point) >= len(best.Point) {
				best = m
			}
		}
	}
	return best, best.ID != 0
}

func writableMount(m storageMount) bool {
	options := strings.Split(m.Options, ",")
	return contains(options, "rw") && !contains(options, "ro")
}

func bindUSBMounts(mounts []storageMount, root string) (*usbStorage, error) {
	jffs, ok := mountForPath(mounts, "/jffs")
	if !ok || jffs.Point != "/jffs" || jffs.Kind != "ext4" || !writableMount(jffs) {
		return nil, fmt.Errorf("USB 版需要先用 USB2JFFS 将 EXT4 U 盘挂载到 /jffs，当前挂载不符合要求")
	}
	data, ok := mountForPath(mounts, root)
	if !ok || data.Kind != "ext4" || data.Device != jffs.Device || !writableMount(data) {
		return nil, fmt.Errorf("软件中心目录没有位于该 U 盘，未写入内置存储")
	}
	return &usbStorage{JFFS: jffs, Root: data, Path: root}, nil
}

func resolveStoragePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return "", err
	}
	resolved, err = resolveStoragePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(abs)), nil
}

func usbTopology(path string) bool {
	usb := regexp.MustCompile(`^usb[0-9]+$`)
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if usb.MatchString(part) {
			return true
		}
	}
	return false
}

func readStorageMounts() ([]storageMount, error) {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	return parseStorageMounts(string(b))
}

func inspectUSBStorage(root string) (*usbStorage, error) {
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("USB 挂载校验仅在 Linux 路由器上可用")
	}
	path, err := resolveStoragePath(root)
	if err != nil {
		return nil, err
	}
	mounts, err := readStorageMounts()
	if err != nil {
		return nil, err
	}
	storage, err := bindUSBMounts(mounts, path)
	if err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[0-9]+:[0-9]+$`).MatchString(storage.JFFS.Device) {
		return nil, fmt.Errorf("U 盘设备编号无效")
	}
	if err = verifyUSBDevice(storage.JFFS, filepath.EvalSymlinks, os.ReadFile); err != nil {
		return nil, err
	}
	return storage, nil
}

func (s *usbStorage) validate(mounts []storageMount, path string) error {
	current, err := bindUSBMounts(mounts, path)
	if err != nil {
		return err
	}
	if path != s.Path || current.JFFS.ID != s.JFFS.ID || current.Root.ID != s.Root.ID || current.JFFS.Device != s.JFFS.Device {
		return fmt.Errorf("U 盘挂载已变化，请确认挂载后重新启动插件管理服务")
	}
	return nil
}

func (a *App) ensureStorage() error {
	if a.usb == nil {
		return nil
	}
	path, err := resolveStoragePath(a.root)
	if err != nil {
		return fmt.Errorf("U 盘存储不可用：%w", err)
	}
	mounts, err := readStorageMounts()
	if err != nil {
		return err
	}
	return a.usb.validate(mounts, path)
}

func CheckStorage(root, mode string) (map[string]any, error) {
	if mode == StorageNative {
		return map[string]any{"mode": mode, "root": root}, nil
	}
	if mode != StorageUSB {
		return nil, fmt.Errorf("存储模式无效")
	}
	storage, err := inspectUSBStorage(root)
	if err != nil {
		return nil, err
	}
	return map[string]any{"mode": mode, "root": storage.Path, "mountPoint": "/jffs", "filesystem": "ext4", "device": storage.JFFS.Device, "usb": true}, nil
}
