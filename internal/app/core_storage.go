// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"

	"github.com/ulikunitz/xz"
)

// Stream verified bytes instead of allocating another complete core in RAM.
func copyVerifiedCore(source, destination, expected string) error {
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(expected) {
		return fmt.Errorf("内核 SHA-256 无效")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(filepath.Dir(destination), ".core-copy-*")
	if err != nil {
		return err
	}
	name := out.Name()
	defer os.Remove(name)
	defer out.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(out, h), io.LimitReader(in, MaxBinarySize+1), make([]byte, 256<<10))
	if err != nil {
		return err
	}
	if n > MaxBinarySize || hex.EncodeToString(h.Sum(nil)) != expected {
		return fmt.Errorf("内核复制校验失败，未替换已有文件")
	}
	if err = out.Chmod(0700); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	return os.Rename(name, destination)
}

func (a *App) storedCoreName(sha string) string {
	if a.storageMode == StorageUSB {
		return "core-" + sha[:24]
	}
	return "corexz-" + sha[:24]
}

func (a *App) storeCore(raw, sha string) (KernelState, error) {
	if err := a.ensureStorage(); err != nil {
		return KernelState{}, err
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(sha) {
		return KernelState{}, fmt.Errorf("内核 SHA-256 无效")
	}
	if a.storageMode != StorageUSB {
		return a.storePackedCore(raw, sha)
	}
	k := KernelState{File: a.storedCoreName(sha), SHA256: sha}
	destination := filepath.Join(a.root, "cores", k.File)
	if filepath.Dir(raw) == filepath.Dir(destination) {
		if err := verifyFile(raw, sha); err != nil {
			return KernelState{}, err
		}
		if err := os.Rename(raw, destination); err != nil {
			return KernelState{}, err
		}
	} else if err := copyVerifiedCore(raw, destination, sha); err != nil {
		return KernelState{}, err
	}
	return k, nil
}

func (a *App) removeUnusedRuntimeCore(sha string) {
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(sha) {
		return
	}
	k := a.snapshot().Kernel
	if k.SHA256 == sha || (k.Previous != nil && k.Previous.SHA256 == sha) {
		return
	}
	name := "mihomo-" + sha[:16]
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	_ = os.Remove(filepath.Join(a.run, name))
}

func verifyStoredCore(path string, k KernelState) error {
	switch k.Compression {
	case "":
		return verifyFile(path, k.SHA256)
	case "xz":
		return verifyFile(path, k.ArchiveSHA256)
	default:
		return fmt.Errorf("不支持的内核存储格式")
	}
}

func unpackCore(path, dest string, k KernelState) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	r, err := (xz.ReaderConfig{DictCap: 16 << 20, SingleStream: true}).NewReader(bufio.NewReaderSize(source, 256<<10))
	if err != nil {
		return fmt.Errorf("内核压缩包损坏：%w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(dest), ".unpack-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, MaxBinarySize+1))
	if err != nil {
		return fmt.Errorf("内核解压失败：%w", err)
	}
	if n > MaxBinarySize || hex.EncodeToString(h.Sum(nil)) != k.SHA256 {
		return fmt.Errorf("解压后的内核大小或 SHA-256 不匹配")
	}
	if err = f.Chmod(0700); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, dest)
}

func (a *App) storePackedCore(raw, sha string) (KernelState, error) {
	f, err := os.Open(raw)
	if err != nil {
		return KernelState{}, err
	}
	defer f.Close()
	stage, err := os.CreateTemp(filepath.Join(a.root, "cores"), ".packed-*")
	if err != nil {
		return KernelState{}, err
	}
	name := stage.Name()
	defer os.Remove(name)
	defer stage.Close()
	h := sha256.New()
	w, err := (xz.WriterConfig{DictCap: 8 << 20}).NewWriter(io.MultiWriter(stage, h))
	if err != nil {
		return KernelState{}, err
	}
	if _, err = io.Copy(w, f); err != nil {
		return KernelState{}, fmt.Errorf("内核压缩写入失败，保留当前内核：%w", err)
	}
	if err = w.Close(); err != nil {
		return KernelState{}, fmt.Errorf("内核压缩写入失败，保留当前内核：%w", err)
	}
	if err = stage.Sync(); err != nil {
		return KernelState{}, err
	}
	if err = stage.Close(); err != nil {
		return KernelState{}, err
	}
	k := KernelState{File: "corexz-" + sha[:24], SHA256: sha, Compression: "xz", ArchiveSHA256: hex.EncodeToString(h.Sum(nil))}
	if err = os.Rename(name, filepath.Join(a.root, "cores", k.File)); err != nil {
		return KernelState{}, err
	}
	return k, nil
}
