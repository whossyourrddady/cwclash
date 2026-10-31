package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestTPROXYPreflightLoadsFirmwareModuleAndNeverHooksTraffic(t *testing.T) {
	for _, mode := range []string{"builtin", "plain", "suffix", "absent"} {
		t.Run(mode, func(t *testing.T) {
			ready := mode == "builtin"
			chains := map[string]bool{}
			loads := 0
			run := func(c Command) error {
				if c.Program == "modprobe" {
					loads++
					if mode == "plain" || mode == "suffix" && c.Args[0] == "xt_TPROXY.ko" {
						ready = true
						return nil
					}
					return fmt.Errorf("module not found")
				}
				if c.Program != "iptables" || c.Args[0] != "-t" || c.Args[1] != "mangle" {
					t.Fatalf("unexpected command: %+v", c)
				}
				op, chain := c.Args[2], c.Args[3]
				if !strings.HasPrefix(chain, "CWCLASH_PT_") {
					t.Fatalf("touched live chain: %+v", c)
				}
				switch op {
				case "-N":
					chains[chain] = true
				case "-A":
					if !ready {
						return fmt.Errorf("No chain/target/match by that name")
					}
				case "-F":
				case "-X":
					delete(chains, chain)
				default:
					t.Fatalf("probe hooked traffic: %+v", c)
				}
				return nil
			}
			err := ensureTPROXY("iptables", 7893, run)
			if (err != nil) != (mode == "absent") {
				t.Fatalf("mode %s error %v", mode, err)
			}
			if len(chains) != 0 {
				t.Fatal("probe leaked a chain")
			}
			if mode == "builtin" && loads != 0 || mode == "suffix" && loads != 2 {
				t.Fatalf("unexpected module loads %d", loads)
			}
		})
	}
}

func TestTPROXYProbeRespectsOwnershipAndCleanupFailure(t *testing.T) {
	count := 0
	err := probeTPROXY("iptables", 7893, func(c Command) error { count++; return fmt.Errorf("already exists") })
	if err == nil || count != 1 {
		t.Fatal("must not clean a chain we did not create")
	}
	loads := 0
	err = ensureTPROXY("iptables", 7893, func(c Command) error {
		if c.Program == "modprobe" {
			loads++
			return nil
		}
		if c.Args[2] == "-F" {
			return fmt.Errorf("cleanup denied")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "清理失败") || loads != 0 {
		t.Fatalf("cleanup failure hidden: %v", err)
	}
}

func TestTPROXYIPv6ChecksBothProtocols(t *testing.T) {
	var protocols []string
	err := probeTPROXY("ip6tables", 7893, func(c Command) error {
		if c.Args[2] == "-A" {
			protocols = append(protocols, c.Args[5])
		}
		return nil
	})
	if err != nil || strings.Join(protocols, ",") != "udp,tcp" {
		t.Fatalf("protocols %v: %v", protocols, err)
	}
}
