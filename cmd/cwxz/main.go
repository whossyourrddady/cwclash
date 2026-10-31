// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ulikunitz/xz"
)

func main() {
	decode := flag.Bool("d", false, "decode an XZ stream")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: cwxz [-d] input output")
		os.Exit(2)
	}
	if err := run(flag.Arg(0), flag.Arg(1), *decode); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(input, output string, decode bool) error {
	src, err := os.Open(input)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(output)
	if err != nil {
		return err
	}
	defer dst.Close()
	if decode {
		r, err := (xz.ReaderConfig{DictCap: 16 << 20, SingleStream: true}).NewReader(bufio.NewReaderSize(src, 256<<10))
		if err != nil {
			return err
		}
		if _, err = io.Copy(dst, r); err != nil {
			return err
		}
	} else {
		w, err := (xz.WriterConfig{DictCap: 8 << 20}).NewWriter(dst)
		if err != nil {
			return err
		}
		if _, err = io.Copy(w, src); err != nil {
			return err
		}
		if err = w.Close(); err != nil {
			return err
		}
	}
	return dst.Close()
}
