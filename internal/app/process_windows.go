// SPDX-License-Identifier: GPL-3.0-or-later
package app

import "os"

func terminate(p *os.Process) error { return p.Kill() }
