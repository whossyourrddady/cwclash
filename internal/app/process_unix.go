// SPDX-License-Identifier: GPL-3.0-or-later
//go:build !windows

package app

import (
	"os"
	"syscall"
)

func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
