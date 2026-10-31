// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const asdCachePath = "/jffs/.asdbk/main_checker.sh_bk"

type ASDCacheReport struct {
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes"`
	Tar    bool   `json:"tar"`
	SHA256 string `json:"sha256,omitempty"`
}
type ASDCacheBackup struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func openASDFile(path string, flag int) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("ASD 检查备份不是普通文件，未操作")
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		f.Close()
		return nil, fmt.Errorf("ASD 检查备份发生变化，请重新检查")
	}
	return f, nil
}

func inspectASD(f *os.File) (ASDCacheReport, error) {
	info, err := f.Stat()
	if err != nil {
		return ASDCacheReport{}, err
	}
	r := ASDCacheReport{Exists: true, Bytes: info.Size()}
	if r.Bytes < 512 || r.Bytes > MaxBinarySize {
		return r, nil
	}
	if _, err = f.Seek(0, 0); err != nil {
		return r, err
	}
	header := make([]byte, 512)
	if _, err = io.ReadFull(f, header); err != nil {
		return r, err
	}
	h, err := tar.NewReader(bytes.NewReader(header)).Next()
	if err != nil || h.Typeflag != tar.TypeDir || !contains([]string{".", "./", "jffs", "jffs/"}, h.Name) {
		return r, nil
	}
	r.Tar = true
	if _, err = f.Seek(0, 0); err != nil {
		return r, err
	}
	digest := sha256.New()
	n, err := io.Copy(digest, io.LimitReader(f, MaxBinarySize+1))
	if err != nil {
		return r, err
	}
	after, err := f.Stat()
	if err != nil {
		return r, err
	}
	if n != info.Size() || !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		return r, fmt.Errorf("ASD 检查备份正在变化，请稍后重新检查")
	}
	r.SHA256 = hex.EncodeToString(digest.Sum(nil))
	return r, nil
}

func cleanASDFile(path, backup string, expected ASDCacheReport) (*ASDCacheBackup, error) {
	f, err := openASDFile(path, os.O_RDWR)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	current, err := inspectASD(f)
	if err != nil {
		return nil, err
	}
	if !current.Tar || expected.SHA256 == "" || current.SHA256 != expected.SHA256 || current.Bytes != expected.Bytes {
		return nil, fmt.Errorf("文件与刚才的检查不一致，未清理；请重新检查")
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil, err
	}
	out, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		out.Close()
		if !keep {
			os.Remove(backup)
		}
	}()
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, digest), io.LimitReader(f, MaxBinarySize+1))
	if err != nil {
		return nil, err
	}
	if err = out.Sync(); err != nil {
		return nil, err
	}
	if err = out.Close(); err != nil {
		return nil, err
	}
	latest, err := inspectASD(f)
	if err != nil {
		return nil, err
	}
	if n != current.Bytes || hex.EncodeToString(digest.Sum(nil)) != current.SHA256 || latest.SHA256 != current.SHA256 {
		return nil, fmt.Errorf("备份时文件发生变化，原文件未清理")
	}
	keep = true
	copy := &ASDCacheBackup{Path: backup, Bytes: n, SHA256: current.SHA256}
	if err = f.Truncate(0); err != nil {
		return copy, err
	}
	if err = f.Sync(); err != nil {
		return copy, err
	}
	return copy, nil
}

func undoASDFile(path string, backup ASDCacheBackup) error {
	if err := verifyFile(backup.Path, backup.SHA256); err != nil {
		return err
	}
	f, err := openASDFile(path, os.O_RDWR)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != 0 {
		return fmt.Errorf("ASD 已生成新的检查备份，未覆盖当前文件")
	}
	source, err := os.Open(backup.Path)
	if err != nil {
		return err
	}
	defer source.Close()
	n, err := io.Copy(f, io.LimitReader(source, MaxBinarySize+1))
	if err != nil || n != backup.Bytes {
		_ = f.Truncate(0)
		return fmt.Errorf("检查备份还原未完成；内存副本仍保留")
	}
	return f.Sync()
}

func (a *App) inspectStorage() (map[string]any, error) {
	if a.dev || runtime.GOOS != "linux" {
		return map[string]any{"supported": false, "mode": a.storageMode, "dataDir": a.coreDataDir(), "note": "存储挂载检查在路由器上可用"}, nil
	}
	a.recoverASDBackup()
	result := map[string]any{"supported": true, "mode": a.storageMode, "root": a.root, "dataDir": a.coreDataDir(), "ready": true, "asdPath": asdCachePath, "cache": ASDCacheReport{}, "undoAvailable": a.asdBackup != nil}
	if a.storageMode == StorageUSB {
		if err := a.ensureStorage(); err != nil {
			result["ready"] = false
			result["note"] = err.Error()
			return result, nil
		}
		result["device"] = a.usb.JFFS.Device
		result["filesystem"] = "ext4"
		result["mountPoint"] = "/jffs"
	}
	if a.asdBackup != nil {
		result["temporaryBackup"] = a.asdBackup.Path
	}
	target := "/jffs"
	if a.storageMode == StorageUSB {
		target = a.root
	}
	lines := strings.Split(strings.TrimSpace(commandOutput("df", "-Pk", target)), "\n")
	if len(lines) > 1 {
		fields := strings.Fields(lines[len(lines)-1])
		if len(fields) >= 6 {
			for key, index := range map[string]int{"totalKiB": 1, "usedKiB": 2, "freeKiB": 3} {
				v, _ := strconv.ParseInt(fields[index], 10, 64)
				result[key] = v
			}
		}
	}
	if a.storageMode == StorageUSB {
		return result, nil
	}
	if info, err := os.Stat("/jffs/ksdb/log"); err == nil {
		result["databaseLogBytes"] = info.Size()
	}
	f, err := openASDFile(asdCachePath, os.O_RDONLY)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	report, err := inspectASD(f)
	if err != nil {
		return nil, err
	}
	result["cache"] = report
	return result, nil
}

func (a *App) cleanASD(expected ASDCacheReport) error {
	if a.storageMode == StorageUSB {
		return fmt.Errorf("USB 版使用 U 盘存储，无需清理内置 ASD 检查备份")
	}
	if a.dev || runtime.GOOS != "linux" {
		return fmt.Errorf("此操作仅在路由器上可用")
	}
	path := filepath.Join(a.run, "asd-before-cleanup-"+randomID(8)+".tar")
	backup, err := cleanASDFile(asdCachePath, path, expected)
	if backup != nil {
		old := a.asdBackup
		a.asdBackup = backup
		b, _ := json.Marshal(backup)
		if saveErr := atomicWrite(filepath.Join(a.run, "asd-cleanup.json"), b, 0600); saveErr != nil {
			return fmt.Errorf("检查备份副本仍保留于 %s，但恢复记录写入失败：%w", backup.Path, saveErr)
		}
		if old != nil && old.Path != backup.Path {
			_ = os.Remove(old.Path)
		}
	}
	if err == nil {
		a.log("info", "ASD 检查备份已备份至内存并清空；安全服务开关和软件中心数据库未更改")
	}
	return err
}

func (a *App) recoverASDBackup() {
	if a.asdBackup != nil {
		return
	}
	b, err := os.ReadFile(filepath.Join(a.run, "asd-cleanup.json"))
	if err != nil || len(b) > 2048 {
		return
	}
	var backup ASDCacheBackup
	if json.Unmarshal(b, &backup) != nil || filepath.Dir(backup.Path) != a.run || !strings.HasPrefix(filepath.Base(backup.Path), "asd-before-cleanup-") || backup.Bytes < 512 || backup.Bytes > MaxBinarySize {
		return
	}
	if verifyFile(backup.Path, backup.SHA256) == nil {
		a.asdBackup = &backup
	}
}
