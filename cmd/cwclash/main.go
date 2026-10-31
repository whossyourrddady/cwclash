// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"cwclash/internal/app"
)

var version = "0.2.1-dev"
var storageMode = app.StorageNative

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cwclash serve|rpc|probe|version")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "storage-check":
		fs := flag.NewFlagSet("storage-check", flag.ExitOnError)
		root := fs.String("root", app.DefaultRoot, "persistent plugin directory")
		mode := fs.String("mode", storageMode, "native or usb-jffs")
		fs.Parse(os.Args[2:])
		result, err := app.CheckStorage(*root, *mode)
		if err != nil {
			fatal(err)
		}
		b, _ := json.Marshal(result)
		fmt.Println(string(b))
	case "health":
		fs := flag.NewFlagSet("health", flag.ExitOnError)
		run := fs.String("runtime", app.DefaultRuntime, "volatile runtime directory")
		fs.Parse(os.Args[2:])
		result := app.Call(*run, "state.get", "e30=")
		if !result.OK {
			fatal(fmt.Errorf("%s", result.Error))
		}
		fmt.Println("管理服务已通过本机接口检查")
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		root := fs.String("root", app.DefaultRoot, "persistent plugin directory")
		run := fs.String("runtime", app.DefaultRuntime, "volatile runtime directory")
		addr := fs.String("listen", app.DefaultListen, "loopback IPC address")
		dev := fs.Bool("dev", false, "local development: no router network changes")
		mode := fs.String("storage-mode", storageMode, "native or usb-jffs")
		fs.Parse(os.Args[2:])
		a, err := app.NewWithStorage(*root, *run, *addr, version, *dev, *mode)
		if err != nil {
			fatal(err)
		}
		ctx, stop := signal.NotifyContext(app.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := a.Serve(ctx); err != nil {
			fatal(err)
		}
	case "rpc":
		if len(os.Args) < 3 {
			fatal(fmt.Errorf("missing action"))
		}
		payload := "e30="
		if len(os.Args) > 3 {
			payload = os.Args[3]
		}
		run := os.Getenv("CWCLASH_RUNTIME")
		if run == "" {
			run = app.DefaultRuntime
		}
		result := app.Call(run, os.Args[2], payload)
		b, _ := json.Marshal(result)
		fmt.Print(base64.StdEncoding.EncodeToString(b))
	case "probe":
		b, _ := json.MarshalIndent(app.Probe(), "", "  ")
		fmt.Println(string(b))
	case "rescue", "watchdog":
		fs := flag.NewFlagSet("rescue", flag.ExitOnError)
		run := fs.String("runtime", app.DefaultRuntime, "owned runtime directory")
		fs.Parse(os.Args[2:])
		var err error
		if os.Args[1] == "watchdog" {
			err = app.Watchdog(*run)
		} else {
			err = app.Rescue(*run)
		}
		if err != nil {
			fatal(err)
		}
	default:
		fatal(fmt.Errorf("unknown command %q", os.Args[1]))
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
