// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Upload struct {
	ID       string
	Kind     string
	Name     string
	Size     int64
	Offset   int64
	SHA256   string
	Path     string
	Metadata json.RawMessage
	Created  time.Time
}

func (a *App) handleUpload(ctx context.Context, action string, p json.RawMessage) (any, error) {
	var q struct {
		ID       string          `json:"id"`
		Kind     string          `json:"kind"`
		Name     string          `json:"name"`
		Size     int64           `json:"size"`
		Offset   int64           `json:"offset"`
		SHA256   string          `json:"sha256"`
		Data     string          `json:"data"`
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(p, &q); err != nil {
		return nil, err
	}
	if action == "upload.begin" {
		if !contains([]string{"profile", "settings", "kernel", "backup", "asset", "request"}, q.Kind) {
			return nil, fmt.Errorf("上传类型无效")
		}
		maximum := int64(MaxArchiveSize)
		if q.Kind == "profile" || q.Kind == "asset" || q.Kind == "settings" {
			maximum = MaxConfigSize
		}
		if q.Kind == "request" {
			maximum = 2 * MaxConfigSize
			var meta struct {
				Action string `json:"action"`
			}
			if json.Unmarshal(q.Metadata, &meta) != nil || !contains([]string{"profiles.save", "settings.save", "devices.save"}, meta.Action) {
				return nil, fmt.Errorf("不支持分块传输此操作")
			}
		}
		if q.Size <= 0 || q.Size > maximum {
			return nil, fmt.Errorf("文件大小不符合要求")
		}
		if q.SHA256 != "" {
			b, err := hex.DecodeString(q.SHA256)
			if err != nil || len(b) != 32 {
				return nil, fmt.Errorf("SHA-256 格式无效")
			}
		}
		id := randomID(12)
		path := filepath.Join(a.run, "transfers", "upload-"+id)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		f.Close()
		upload := &Upload{ID: id, Kind: q.Kind, Name: filepath.Base(q.Name), Size: q.Size, SHA256: q.SHA256, Path: path, Metadata: q.Metadata, Created: time.Now()}
		a.mu.Lock()
		defer a.mu.Unlock()
		if len(a.uploads) >= 6 {
			os.Remove(path)
			return nil, fmt.Errorf("同时上传的文件过多")
		}
		a.uploads[id] = upload
		return map[string]any{"id": id, "chunkSize": 768}, nil
	}
	a.mu.Lock()
	upload := a.uploads[q.ID]
	if upload == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("上传不存在或已结束")
	}
	switch action {
	case "upload.chunk":
		defer a.mu.Unlock()
		b, err := base64.StdEncoding.DecodeString(q.Data)
		if err != nil || len(b) > 64<<10 || len(b) == 0 {
			return nil, fmt.Errorf("数据分块无效")
		}
		if q.Offset != upload.Offset || upload.Offset+int64(len(b)) > upload.Size {
			return nil, fmt.Errorf("数据分块位置不正确，请重新上传")
		}
		f, err := os.OpenFile(upload.Path, os.O_WRONLY, 0600)
		if err != nil {
			return nil, err
		}
		n, err := f.WriteAt(b, q.Offset)
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		upload.Offset += int64(n)
		return map[string]any{"offset": upload.Offset}, nil
	case "upload.cancel":
		delete(a.uploads, q.ID)
		a.mu.Unlock()
		return nil, os.Remove(upload.Path)
	case "upload.finish":
		if upload.Offset != upload.Size {
			a.mu.Unlock()
			return nil, fmt.Errorf("文件尚未上传完整")
		}
		delete(a.uploads, q.ID)
		a.mu.Unlock()
		copy := *upload
		return a.newJob("upload."+upload.Kind, func() (any, error) {
			defer os.Remove(copy.Path)
			if err := a.ensureStorage(); err != nil {
				return nil, err
			}
			if copy.SHA256 != "" {
				if err := verifyFile(copy.Path, copy.SHA256); err != nil {
					return nil, err
				}
			}
			if copy.Kind == "request" {
				var meta struct {
					Action string `json:"action"`
				}
				if json.Unmarshal(copy.Metadata, &meta) != nil || !contains([]string{"profiles.save", "settings.save", "devices.save"}, meta.Action) {
					return nil, fmt.Errorf("不支持分块传输此操作")
				}
				b, err := os.ReadFile(copy.Path)
				if err != nil {
					return nil, err
				}
				return a.dispatch(a.operationContext(), meta.Action, b)
			}
			a.op.Lock()
			defer a.op.Unlock()
			switch copy.Kind {
			case "settings":
				b, err := os.ReadFile(copy.Path)
				if err != nil {
					return nil, err
				}
				var settings Settings
				if err = json.Unmarshal(b, &settings); err != nil {
					return nil, err
				}
				if err = validateSettings(settings); err != nil {
					return nil, err
				}
				s := a.snapshot()
				s.Settings = settings
				return nil, a.applyState(s)
			case "profile":
				var profile Profile
				if err := json.Unmarshal(copy.Metadata, &profile); err != nil {
					return nil, err
				}
				b, err := os.ReadFile(copy.Path)
				if err != nil {
					return nil, err
				}
				return a.saveProfile(a.operationContext(), profile, string(b))
			case "kernel":
				return nil, a.installCoreArchive(a.operationContext(), copy.Path, "")
			case "backup":
				return nil, a.restoreBackup(copy.Path)
			case "asset":
				var meta struct {
					Path string `json:"path"`
				}
				if json.Unmarshal(copy.Metadata, &meta) != nil {
					return nil, fmt.Errorf("资源路径无效")
				}
				clean, err := safeRelative(meta.Path)
				if err != nil {
					return nil, err
				}
				b, err := os.ReadFile(copy.Path)
				if err != nil {
					return nil, err
				}
				return nil, atomicWrite(filepath.Join(a.root, "assets", clean), b, 0600)
			}
			return nil, fmt.Errorf("上传类型无效")
		}), nil
	default:
		a.mu.Unlock()
		return nil, fmt.Errorf("上传操作无效")
	}
}

func (a *App) handleDownload(action string, p json.RawMessage) (any, error) {
	var q struct {
		Kind   string `json:"kind"`
		ID     string `json:"id"`
		Offset int64  `json:"offset"`
	}
	if err := json.Unmarshal(p, &q); err != nil {
		return nil, err
	}
	if action == "download.begin" {
		id := randomID(12)
		path := filepath.Join(a.run, "transfers", "download-"+id)
		name := ""
		switch q.Kind {
		case "backup":
			a.op.Lock()
			err := a.createBackup(path)
			a.op.Unlock()
			if err != nil {
				return nil, err
			}
			name = "cwclash-backup-" + time.Now().Format("20060102-150405") + ".zip"
		case "profile":
			if !validID(q.ID) {
				return nil, fmt.Errorf("配置标识无效")
			}
			b, err := os.ReadFile(filepath.Join(a.root, "profiles", q.ID+".yaml"))
			if err != nil {
				return nil, err
			}
			if err = atomicWrite(path, b, 0600); err != nil {
				return nil, err
			}
			name = "clash-" + q.ID + ".yaml"
		default:
			return nil, fmt.Errorf("下载类型无效")
		}
		stat, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		a.mu.Lock()
		if len(a.downloads) > 12 {
			a.mu.Unlock()
			os.Remove(path)
			return nil, fmt.Errorf("下载任务过多")
		}
		a.downloads[id] = path
		a.mu.Unlock()
		return map[string]any{"id": id, "name": name, "size": stat.Size(), "chunkSize": 48 << 10}, nil
	}
	a.mu.RLock()
	path := a.downloads[q.ID]
	a.mu.RUnlock()
	if path == "" {
		return nil, fmt.Errorf("下载不存在或已结束")
	}
	if action == "download.cancel" {
		a.mu.Lock()
		delete(a.downloads, q.ID)
		a.mu.Unlock()
		return nil, os.Remove(path)
	}
	if action != "download.chunk" || q.Offset < 0 {
		return nil, fmt.Errorf("下载参数无效")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, 48<<10)
	n, err := f.ReadAt(buf, q.Offset)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return map[string]any{"data": base64.StdEncoding.EncodeToString(buf[:n]), "offset": q.Offset + int64(n), "done": err == io.EOF}, nil
}

func (a *App) createBackup(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	ok := false
	defer func() {
		zw.Close()
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	state := a.snapshot()
	state.Enabled = false
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0600)
		entry, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	if err = write("state.json", b); err != nil {
		return err
	}
	var total int64
	for _, p := range state.Profiles {
		if !validID(p.ID) {
			return fmt.Errorf("配置标识损坏")
		}
		b, err := os.ReadFile(filepath.Join(a.root, "profiles", p.ID+".yaml"))
		if err != nil {
			return err
		}
		total += int64(len(b))
		if total > 32<<20 {
			return fmt.Errorf("备份配置总大小超过 32 MiB")
		}
		if err = write("profiles/"+p.ID+".yaml", b); err != nil {
			return err
		}
	}
	assets := filepath.Join(a.root, "assets")
	if _, err = os.Stat(assets); err == nil {
		err = filepath.WalkDir(assets, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("资源目录含符号链接，未加入备份")
			}
			relative, err := filepath.Rel(assets, path)
			if err != nil {
				return err
			}
			stat, err := entry.Info()
			if err != nil {
				return err
			}
			total += stat.Size()
			if total > 32<<20 {
				return fmt.Errorf("备份配置总大小超过 32 MiB")
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return write("assets/"+filepath.ToSlash(relative), b)
		})
		if err != nil {
			return err
		}
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func (a *App) restoreBackup(path string) error {
	if err := a.ensureStorage(); err != nil {
		return err
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("备份不是有效的 ZIP 文件")
	}
	defer z.Close()
	if len(z.File) > 512 {
		return fmt.Errorf("备份文件数量过多")
	}
	files := map[string][]byte{}
	var total uint64
	for _, entry := range z.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("备份中不允许符号链接")
		}
		clean, err := safeRelative(entry.Name)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(clean)
		if name != "state.json" && !strings.HasPrefix(name, "profiles/") && !strings.HasPrefix(name, "assets/") {
			return fmt.Errorf("备份含不支持的文件：%s", name)
		}
		total += entry.UncompressedSize64
		if total > 32<<20 || entry.UncompressedSize64 > MaxConfigSize {
			return fmt.Errorf("备份解压大小超出限制")
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(reader, MaxConfigSize+1))
		reader.Close()
		if err != nil {
			return err
		}
		if len(b) > MaxConfigSize {
			return fmt.Errorf("备份文件过大")
		}
		if _, exists := files[name]; exists {
			return fmt.Errorf("备份包含重复文件")
		}
		files[name] = b
	}
	var next State
	if err = json.Unmarshal(files["state.json"], &next); err != nil {
		return fmt.Errorf("备份缺少有效的 state.json")
	}
	if next.Schema != 1 {
		return fmt.Errorf("备份配置版本不受支持")
	}
	if err = validateSettings(next.Settings); err != nil {
		return err
	}
	current := a.snapshot()
	next.Kernel = current.Kernel
	next.Enabled = false
	if err = validateState(next); err != nil {
		return err
	}
	// Validate against the incoming assets without replacing any live files.
	stage, err := os.MkdirTemp(a.run, "restore-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	for name, b := range files {
		if strings.HasPrefix(name, "assets/") {
			if err = atomicWrite(filepath.Join(stage, filepath.FromSlash(name)), b, 0600); err != nil {
				return err
			}
		}
	}
	validationApp := &App{root: stage, run: stage, dev: a.dev, secret: a.secret, state: next}
	for _, p := range next.Profiles {
		if !validID(p.ID) {
			return fmt.Errorf("备份配置标识无效")
		}
		b, ok := files["profiles/"+p.ID+".yaml"]
		if !ok {
			return fmt.Errorf("备份缺少配置：%s", p.Name)
		}
		compiled, err := validationApp.compileConfig(next, b, next.Settings.ControllerPort)
		if err != nil {
			return err
		}
		if err = a.validateConfigIn(a.operationContext(), compiled, next.Kernel, filepath.Join(stage, "data"), filepath.Join(stage, "assets")); err != nil {
			return err
		}
	}
	if next.ActiveProfile != "" && findProfile(next, next.ActiveProfile) == nil {
		return fmt.Errorf("备份中的当前配置不存在")
	}
	// Create a rollback archive before changing any active configuration.
	rollback := filepath.Join(a.root, "restore-previous.zip")
	rollbackStage := filepath.Join(a.root, ".restore-backup-"+randomID(8)+".zip")
	defer os.Remove(rollbackStage)
	if err = a.createBackup(rollbackStage); err != nil {
		return err
	}
	if err = os.Rename(rollbackStage, rollback); err != nil {
		return err
	}
	type oldFile struct {
		data   []byte
		exists bool
	}
	originals := map[string]oldFile{}
	names := []string{}
	for name := range files {
		if name == "state.json" {
			continue
		}
		names = append(names, name)
		b, readErr := os.ReadFile(filepath.Join(a.root, filepath.FromSlash(name)))
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		originals[name] = oldFile{data: b, exists: readErr == nil}
	}
	sort.Strings(names)
	wasRunning := a.isRunning()
	if err = a.stopService(false); err != nil {
		return err
	}
	written := []string{}
	undo := func(cause error) error {
		var restoreErrors []string
		for i := len(written) - 1; i >= 0; i-- {
			name := written[i]
			old := originals[name]
			path := filepath.Join(a.root, filepath.FromSlash(name))
			var restoreErr error
			if old.exists {
				restoreErr = atomicWrite(path, old.data, 0600)
			} else {
				restoreErr = os.Remove(path)
			}
			if restoreErr != nil {
				restoreErrors = append(restoreErrors, restoreErr.Error())
			}
		}
		if wasRunning && len(restoreErrors) == 0 {
			if restoreErr := a.startService(false); restoreErr != nil {
				restoreErrors = append(restoreErrors, restoreErr.Error())
			}
		}
		if len(restoreErrors) > 0 {
			a.setFailure("恢复回滚未完全完成，已撤销接管；恢复前备份保留于 restore-previous.zip")
			return fmt.Errorf("备份恢复失败，自动回退未完全完成；请使用 restore-previous.zip 恢复：%w（%s）", cause, strings.Join(restoreErrors, "；"))
		}
		return fmt.Errorf("备份恢复失败，原配置文件已回退，恢复前备份位于 restore-previous.zip：%w", cause)
	}
	for _, name := range names {
		if err = atomicWrite(filepath.Join(a.root, filepath.FromSlash(name)), files[name], 0600); err != nil {
			return undo(err)
		}
		written = append(written, name)
	}
	if err = a.persist(next); err != nil {
		return undo(err)
	}
	a.log("info", "配置备份已恢复；代理保持关闭，请检查后启用")
	return nil
}

func hashBytes(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
