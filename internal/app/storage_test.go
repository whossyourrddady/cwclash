// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPackedCoreCachesVerifiedBinaryInRAM(t *testing.T) {
	a := testApp(t)
	raw := bytes.Repeat([]byte("cwclash-verified-binary\x00"), 5000)
	source := filepath.Join(a.run, "binary-fixture")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	k, err := a.storePackedCore(source, hashBytes(raw))
	if err != nil {
		t.Fatal(err)
	}
	path, err := a.runtimeCore(k)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, raw) {
		t.Fatal("RAM executable does not match original")
	}
	wrong := k
	wrong.SHA256 = hashBytes([]byte("different binary"))
	if _, err = a.runtimeCore(wrong); err == nil {
		t.Fatal("accepted wrong uncompressed digest")
	}
	archive := filepath.Join(a.root, "cores", k.File)
	f, err := os.OpenFile(archive, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("tampered"))
	f.Close()
	if _, err = a.runtimeCore(k); err == nil {
		t.Fatal("accepted modified persistent archive despite cached executable")
	}
}

func makeASDFixture(t *testing.T, path string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	if err := w.WriteHeader(&tar.Header{Name: "./", Typeflag: tar.TypeDir, Mode: 0700}); err != nil {
		t.Fatal(err)
	}
	payload := []byte("only a diagnostic cache fixture")
	if err := w.WriteHeader(&tar.Header{Name: "fixture.txt", Mode: 0600, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(payload)
	_ = w.Close()
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func readASDFixture(t *testing.T, path string) ASDCacheReport {
	t.Helper()
	f, err := openASDFile(path, os.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := inspectASD(f)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestASDCleanupCanRestoreExactVerifiedCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache")
	raw := makeASDFixture(t, path)
	r := readASDFixture(t, path)
	if !r.Tar || r.SHA256 == "" {
		t.Fatal("did not identify tar cache")
	}
	backup, err := cleanASDFile(path, filepath.Join(dir, "rollback"), r)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Size() != 0 {
		t.Fatal("cache was not cleared")
	}
	if err = undoASDFile(path, *backup); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, raw) {
		t.Fatal("restore changed diagnostic cache")
	}
}

func TestASDCleanupRejectsChangedFileAndFailedBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache")
	raw := makeASDFixture(t, path)
	r := readASDFixture(t, path)
	changed := append(append([]byte(nil), raw...), []byte("changed")...)
	_ = os.WriteFile(path, changed, 0600)
	if _, err := cleanASDFile(path, filepath.Join(dir, "rollback"), r); err == nil {
		t.Fatal("cleared file that changed after inspection")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, changed) {
		t.Fatal("changed cache was modified")
	}
	_ = os.WriteFile(path, raw, 0600)
	_ = os.Mkdir(filepath.Join(dir, "blocked"), 0700)
	if _, err := cleanASDFile(path, filepath.Join(dir, "blocked"), r); err == nil {
		t.Fatal("cleared despite backup failure")
	}
	actual, _ = os.ReadFile(path)
	if !bytes.Equal(actual, raw) {
		t.Fatal("cache changed when backup could not be created")
	}
}

func TestASDCleanupDoesNotTouchScriptsOrNewCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache")
	script := bytes.Repeat([]byte("#!/bin/sh\necho active\n"), 80)
	_ = os.WriteFile(path, script, 0600)
	r := readASDFixture(t, path)
	if _, err := cleanASDFile(path, filepath.Join(dir, "script-backup"), r); err == nil {
		t.Fatal("accepted an executable script instead of tar cache")
	}
	_ = makeASDFixture(t, path)
	r = readASDFixture(t, path)
	backup, err := cleanASDFile(path, filepath.Join(dir, "rollback"), r)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(path, []byte("a newly generated cache"), 0600)
	if err = undoASDFile(path, *backup); err == nil {
		t.Fatal("overwrote newly generated ASD data")
	}
}
