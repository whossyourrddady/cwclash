package app

import (
	"errors"
	"fmt"
	"strconv"
)

type tproxyCleanupError struct{ error }

// The probe chain has no jump from any built-in chain: no traffic is captured.
// Only delete a chain after our own -N succeeded, including on probe failure.
func probeTPROXY(program string, port int, run func(Command) error) (err error) {
	chain := "CWCLASH_PT_" + randomID(5)
	call := func(args ...string) error { return run(Command{program, append([]string{"-t", "mangle"}, args...)}) }
	if err = call("-N", chain); err != nil {
		return err
	}
	defer func() {
		if cleanup := errors.Join(call("-F", chain), call("-X", chain)); cleanup != nil {
			err = &tproxyCleanupError{errors.Join(err, fmt.Errorf("TPROXY 检测链清理失败：%w", cleanup))}
		}
	}()
	protocols := []string{"udp"}
	if program == "ip6tables" {
		protocols = append(protocols, "tcp")
	}
	for _, protocol := range protocols {
		if err = call("-A", chain, "-p", protocol, "-j", "TPROXY", "--on-port", strconv.Itoa(port), "--tproxy-mark", fmt.Sprintf("0x%x/0x%x", ProxyMark, MarkMask)); err != nil {
			return err
		}
	}
	return nil
}

func ensureTPROXY(program string, port int, run func(Command) error) error {
	if err := probeTPROXY(program, port, run); err == nil {
		return nil
	} else if cleanup := new(tproxyCleanupError); errors.As(err, &cleanup) {
		return err
	}
	// ASUS BusyBox variants differ on whether modprobe expects the .ko suffix.
	// Use the firmware module resolver; never download/force-load foreign modules.
	var attempts []error
	for _, module := range []string{"xt_TPROXY", "xt_TPROXY.ko"} {
		loadErr := run(Command{"modprobe", []string{module}})
		probeErr := probeTPROXY(program, port, run)
		if probeErr == nil {
			return nil
		}
		if cleanup := new(tproxyCleanupError); errors.As(probeErr, &cleanup) {
			return probeErr
		}
		attempts = append(attempts, loadErr, probeErr)
	}
	return fmt.Errorf("%s 内核 TPROXY 规则检测失败，已尝试加载固件自带 xt_TPROXY 模块；未接管流量。请查看环境诊断的内核/模块信息：%w", program, errors.Join(attempts...))
}
