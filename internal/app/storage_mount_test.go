// SPDX-License-Identifier: GPL-3.0-or-later
package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const usbMountFixture = `21 1 0:1 / / rw - rootfs rootfs rw
40 21 31:5 / /jffs rw - ubifs ubi:jffs rw
51 21 8:1 / /tmp/mnt/USB\040drive rw - ext4 /dev/sda1 rw
52 21 8:1 / /jffs rw - ext4 /dev/sda1 rw
`

func TestUSBMountBindingRejectsFallbackAndReplacement(t *testing.T) {
	mounts, err := parseStorageMounts(usbMountFixture)
	if err != nil {
		t.Fatal(err)
	}
	if mounts[2].Point != "/tmp/mnt/USB drive" {
		t.Fatal("mount escape was not decoded")
	}
	bound, err := bindUSBMounts(mounts, "/jffs/.koolshare/cwclash")
	if err != nil {
		t.Fatal(err)
	}
	journal, _ := json.Marshal(bound)
	if err = verifyRuntimeStorageBinding(journal, mounts); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"internal fallback":         strings.ReplaceAll(usbMountFixture, "52 21 8:1 / /jffs rw - ext4 /dev/sda1 rw\n", ""),
		"read only":                 strings.ReplaceAll(usbMountFixture, "52 21 8:1 / /jffs rw", "52 21 8:1 / /jffs ro"),
		"new mount identity":        strings.ReplaceAll(usbMountFixture, "52 21 8:1", "99 21 8:1"),
		"different device":          strings.ReplaceAll(usbMountFixture, "8:1", "8:2"),
		"nested internal directory": usbMountFixture + "53 52 0:7 / /jffs/.koolshare rw - tmpfs tmpfs rw\n",
	} {
		t.Run(name, func(t *testing.T) {
			current, err := parseStorageMounts(data)
			if err != nil {
				t.Fatal(err)
			}
			if bound.validate(current, bound.Path) == nil {
				t.Fatal("unsafe storage accepted")
			}
			if verifyRuntimeStorageBinding(journal, current) == nil {
				t.Fatal("RAM watchdog journal accepted unsafe storage")
			}
		})
	}
	if _, err = bindUSBMounts(mounts, "/internal/cwclash"); err == nil {
		t.Fatal("internal plugin directory accepted")
	}
	if !usbTopology("/devices/platform/usb1/1-1/block/sda/sda1") || usbTopology("/devices/pci0000/block/sda/sda1") {
		t.Fatal("USB topology detection failed")
	}
}

func TestUSBProductionRejectsDestinationBeforeCreatingFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "not-created")
	if _, err := NewWithStorage(root, filepath.Join(base, "run"), "127.0.0.1:0", "test", false, StorageUSB); err == nil {
		t.Fatal("non-USB destination accepted")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("rejected install created persistent files")
	}
}

func TestUSBLayoutKeepsCacheAndUsesVerifiedRawCore(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "usb")
	run := filepath.Join(base, "ram")
	a, err := NewWithStorage(root, run, "127.0.0.1:0", "test", true, StorageUSB)
	if err != nil {
		t.Fatal(err)
	}
	if a.coreDataDir() != filepath.Join(root, "data") {
		t.Fatal("USB cache fell back to RAM")
	}
	data := bytes.Repeat([]byte("controlled-core-fixture\n"), 8192)
	raw := filepath.Join(run, "candidate")
	if err = os.WriteFile(raw, data, 0600); err != nil {
		t.Fatal(err)
	}
	sha := hashBytes(data)
	kernel, err := a.storeCore(raw, sha)
	if err != nil {
		t.Fatal(err)
	}
	if kernel.Compression != "" || kernel.File != "core-"+sha[:24] {
		t.Fatal("USB core was recompressed")
	}
	if err = verifyStoredCore(filepath.Join(root, "cores", kernel.File), kernel); err != nil {
		t.Fatal(err)
	}
	runtimeFile, err := a.runtimeCore(kernel)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(runtimeFile); !bytes.Equal(b, data) {
		t.Fatal("RAM copy changed core bytes")
	}
	cache := filepath.Join(a.coreDataDir(), "providers", "cached.yaml")
	if err = os.WriteFile(cache, []byte("payload: [example.test]"), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := a.storePackedCore(raw, sha)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Version = "v1.19.33"
	kernel.Version = legacy.Version
	state := a.snapshot()
	state.Kernel = legacy
	if err = a.persist(state); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(kernel)
	if err = os.WriteFile(filepath.Join(root, "bundled-kernel.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewWithStorage(root, filepath.Join(base, "new-ram"), "127.0.0.1:0", "test", true, StorageUSB)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.snapshot().Kernel.Compression != "" {
		t.Fatal("identical packed kernel was not moved to raw USB storage")
	}
	if b, err = os.ReadFile(cache); err != nil || len(b) == 0 {
		t.Fatal("USB cache did not survive runtime replacement")
	}
}

func TestVerifiedCoreCopyDoesNotReplaceOnCorruption(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	destination := filepath.Join(base, "working")
	if err := os.WriteFile(source, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("previous-good"), 0600); err != nil {
		t.Fatal(err)
	}
	if copyVerifiedCore(source, destination, hashBytes([]byte("expected"))) == nil {
		t.Fatal("corrupt core accepted")
	}
	if b, _ := os.ReadFile(destination); string(b) != "previous-good" {
		t.Fatal("working core was replaced")
	}
}
