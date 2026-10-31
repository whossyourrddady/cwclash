// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Release struct {
	Version   string `json:"version"`
	Published string `json:"published"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	Page      string `json:"page"`
}

func parseRelease(data []byte) (Release, error) {
	var source struct {
		Tag        string `json:"tag_name"`
		Published  string `json:"published_at"`
		HTML       string `json:"html_url"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		return Release{}, err
	}
	if source.Prerelease || source.Draft || !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(source.Tag) {
		return Release{}, fmt.Errorf("发布元数据不是稳定版本")
	}
	for _, asset := range source.Assets {
		if asset.Name == "mihomo-linux-arm64-"+source.Tag+".gz" {
			checksum := strings.TrimPrefix(asset.Digest, "sha256:")
			if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(checksum) {
				return Release{}, fmt.Errorf("官方发布未提供 SHA-256，未执行更新")
			}
			prefix := "https://github.com/MetaCubeX/mihomo/releases/download/" + source.Tag + "/"
			if !strings.HasPrefix(asset.URL, prefix) {
				return Release{}, fmt.Errorf("内核下载地址不是官方发布资源")
			}
			if asset.Size <= 0 || asset.Size > MaxArchiveSize {
				return Release{}, fmt.Errorf("内核发布大小无效")
			}
			return Release{Version: source.Tag, Published: source.Published, URL: asset.URL, SHA256: checksum, Size: asset.Size, Page: source.HTML}, nil
		}
	}
	return Release{}, fmt.Errorf("官方发布缺少 Linux ARM64 内核")
}

func (a *App) checkRelease(ctx context.Context) (Release, error) {
	b, _, err := a.fetch(ctx, "https://api.github.com/repos/MetaCubeX/mihomo/releases/latest", "cwclash/"+a.version, 2<<20)
	if err != nil {
		return Release{}, err
	}
	release, err := parseRelease(b)
	if err != nil {
		return release, err
	}
	a.mu.Lock()
	a.latestRelease = &release
	a.lastUpdateCheck = time.Now()
	a.mu.Unlock()
	return release, nil
}

func (a *App) upgradeCore(ctx context.Context) error {
	if a.dev {
		return fmt.Errorf("在线升级包用于 ARM64 路由器；本地开发请预置对应平台内核")
	}
	release, err := a.checkRelease(ctx)
	if err != nil {
		return err
	}
	if release.Version == a.snapshot().Kernel.Version {
		return fmt.Errorf("已经是最新稳定内核 %s", release.Version)
	}
	a.log("info", "正在下载官方 mihomo "+release.Version)
	b, _, err := a.fetch(ctx, release.URL, "cwclash/"+a.version, MaxArchiveSize)
	if err != nil {
		return err
	}
	if int64(len(b)) != release.Size || hashBytes(b) != release.SHA256 {
		return fmt.Errorf("下载内核大小或 SHA-256 不匹配，保留当前内核")
	}
	path := filepath.Join(a.run, "transfers", ".download-"+randomID(8)+".gz")
	if err = atomicWrite(path, b, 0600); err != nil {
		return err
	}
	defer os.Remove(path)
	return a.installCoreArchive(ctx, path, release.Version)
}

func (a *App) installCoreArchive(ctx context.Context, path, expectedVersion string) error {
	if err := a.ensureStorage(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("请上传官方 mihomo-linux-arm64-版本.gz 文件")
	}
	defer z.Close()
	stageDir := a.run
	if a.storageMode == StorageUSB {
		stageDir = filepath.Join(a.root, "cores")
	}
	stage, err := os.CreateTemp(stageDir, ".core-stage-*")
	if err != nil {
		return err
	}
	stageName := stage.Name()
	defer os.Remove(stageName)
	digest := sha256.New()
	n, err := io.Copy(io.MultiWriter(stage, digest), io.LimitReader(z, MaxBinarySize+1))
	if err != nil {
		stage.Close()
		return err
	}
	if n > MaxBinarySize {
		stage.Close()
		return fmt.Errorf("内核解压大小超出限制")
	}
	if err = stage.Sync(); err != nil {
		stage.Close()
		return err
	}
	if err = stage.Close(); err != nil {
		return err
	}
	if err = validateELF(stageName); err != nil {
		return err
	}
	sha := hex.EncodeToString(digest.Sum(nil))
	previous := a.snapshot()
	if previous.Kernel.SHA256 == sha {
		return fmt.Errorf("当前已使用此内核")
	}
	defer a.removeUnusedRuntimeCore(sha)
	probeBinary := stageName
	if a.storageMode == StorageUSB {
		probeBinary = filepath.Join(a.run, "mihomo-"+sha[:16])
		if err = copyVerifiedCore(stageName, probeBinary, sha); err != nil {
			return err
		}
	}
	if err = os.Chmod(probeBinary, 0700); err != nil {
		return err
	}
	versionCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	output, err := exec.CommandContext(versionCtx, probeBinary, "-v").CombinedOutput()
	if err != nil {
		return fmt.Errorf("内核无法在此路由器运行：%s", redact(string(output)))
	}
	version, err := parseKernelVersion(string(output))
	if err != nil {
		return err
	}
	if expectedVersion != "" && version != expectedVersion {
		return fmt.Errorf("内核版本与发布元数据不匹配")
	}
	_, priorArchiveErr := os.Stat(filepath.Join(a.root, "cores", a.storedCoreName(sha)))
	candidate, err := a.storeCore(stageName, sha)
	if err != nil {
		return err
	}
	if os.IsNotExist(priorArchiveErr) {
		defer a.removeUnusedCoreFile(candidate.File)
	}
	candidate.Version = version
	if previous.Kernel.File != "" {
		old := previous.Kernel
		old.Previous = nil
		candidate.Previous = &old
	}
	if previous.ActiveProfile != "" {
		config, err := a.prepareConfig(previous)
		if err != nil {
			return err
		}
		compiled, err := os.ReadFile(config)
		if err != nil {
			return err
		}
		if err = a.validateConfig(ctx, compiled, candidate); err != nil {
			return fmt.Errorf("新内核未通过当前配置检查：%w", err)
		}
	}
	wasRunning := a.isRunning()
	if wasRunning {
		if err = a.stopService(false); err != nil {
			return err
		}
	}
	next := previous
	next.Kernel = candidate
	if err = a.persist(next); err != nil {
		if wasRunning {
			_ = a.startService(false)
		}
		return err
	}
	if wasRunning {
		if err = a.startService(false); err != nil {
			if restoreErr := a.persist(previous); restoreErr != nil {
				_ = a.stopService(false)
				a.setFailure("新内核启动失败，上一状态无法写回，代理保持关闭：" + restoreErr.Error())
				return fmt.Errorf("新内核启动失败：%w；上一状态恢复失败：%s", err, restoreErr)
			}
			restoreErr := a.startService(false)
			if restoreErr != nil {
				a.setFailure("新内核和旧内核均未能启动，已恢复直连：" + restoreErr.Error())
			}
			return fmt.Errorf("新内核启动失败，已尝试回退：%w", err)
		}
	}
	a.log("info", "mihomo 已更新至 "+version)
	if previous.Kernel.Previous != nil {
		a.removeUnusedCoreFile(previous.Kernel.Previous.File)
		a.removeUnusedRuntimeCore(previous.Kernel.Previous.SHA256)
	}
	return nil
}

func (a *App) removeUnusedCoreFile(file string) {
	if !validID(file) {
		return
	}
	k := a.snapshot().Kernel
	if k.File == file || (k.Previous != nil && k.Previous.File == file) {
		return
	}
	_ = os.Remove(filepath.Join(a.root, "cores", file))
}

func validateELF(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("内核不是有效的 Linux ELF 文件")
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS64 || f.Machine != elf.EM_AARCH64 || f.Data != elf.ELFDATA2LSB {
		return fmt.Errorf("内核必须是 Linux ARM64 小端构建")
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			return fmt.Errorf("内核必须静态链接，以兼容路由器运行库")
		}
	}
	return nil
}

func parseKernelVersion(output string) (string, error) {
	if !strings.Contains(strings.ToLower(output), "mihomo") {
		return "", fmt.Errorf("该程序不是 mihomo 内核")
	}
	stable := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	for _, version := range strings.Fields(output) {
		if stable.MatchString(version) {
			return version, nil
		}
	}
	return "", fmt.Errorf("无法识别 mihomo 稳定版本号")
}

func (a *App) rollbackCore() error {
	s := a.snapshot()
	if s.Kernel.Previous == nil {
		return fmt.Errorf("没有可回退的内核")
	}
	old := *s.Kernel.Previous
	if err := verifyStoredCore(filepath.Join(a.root, "cores", old.File), old); err != nil {
		return err
	}
	if s.ActiveProfile != "" {
		path, err := a.prepareConfig(s)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = a.validateConfig(a.operationContext(), b, old); err != nil {
			return err
		}
	}
	current := s.Kernel
	current.Previous = nil
	old.Previous = &current
	next := s
	next.Kernel = old
	wasRunning := a.isRunning()
	if wasRunning {
		if err := a.stopService(false); err != nil {
			return err
		}
	}
	if err := a.persist(next); err != nil {
		if wasRunning {
			_ = a.startService(false)
		}
		return err
	}
	if wasRunning {
		if err := a.startService(false); err != nil {
			_ = a.persist(s)
			_ = a.startService(false)
			return err
		}
	}
	a.log("info", "内核已回退至 "+old.Version)
	return nil
}
