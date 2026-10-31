// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: cwpack stage-root output.tar.gz")
		os.Exit(2)
	}
	if err := pack(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func pack(stage, output string) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	gz.Header.ModTime = time.Unix(0, 0)
	tw := tar.NewWriter(gz)
	err = filepath.WalkDir(stage, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("unexpected symlink %s", relative)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.ModTime = time.Unix(0, 0)
		header.AccessTime = time.Time{}
		header.ChangeTime = time.Time{}
		header.Uid = 0
		header.Gid = 0
		header.Uname = "root"
		header.Gname = "root"
		header.Mode = 0644
		if d.IsDir() {
			header.Mode = 0755
			header.Name += "/"
		} else if strings.HasSuffix(relative, ".sh") {
			header.Mode = 0755
		}
		if err = tw.WriteHeader(header); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, src)
		closeErr := src.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	return f.Close()
}
