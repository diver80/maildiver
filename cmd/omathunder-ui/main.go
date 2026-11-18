// Command omathunder-ui is the native OmaThunder desktop app (main window + menu bar / tray widget).
// It talks to Thunderbird only through the local bridge of the omathunder Native Messaging host,
// so it is built separately: the host binary stays free of GUI dependencies.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/nativeui"
)

var version = "0.1.0-dev"

func main() {
	native.Version = version
	native.LoadEnvFile()

	fs := flag.NewFlagSet("omathunder-ui", flag.ExitOnError)
	minimized := fs.Bool("minimized", false, "Nur als Menüleisten-/Tray-Status-Widget starten")
	noTray := fs.Bool("no-tray", false, "Menüleisten-/Tray-Status-Widget deaktivieren")
	bridgeFile := fs.String("bridge-file", "", "Optionaler Pfad zu bridge.json")
	showVersion := fs.Bool("version", false, "Version ausgeben")
	_ = fs.Parse(filterLaunchArgs(os.Args[1:]))

	if *showVersion {
		fmt.Println("omathunder-ui", version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := nativeui.Run(ctx, nativeui.Options{
		DiscoveryPath: *bridgeFile,
		StartHidden:   *minimized,
		DisableTray:   *noTray,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
		os.Exit(1)
	}
}

// filterLaunchArgs drops the -psn_* process serial number older macOS versions pass to apps
// started from Finder, which the flag package would reject.
func filterLaunchArgs(args []string) []string {
	out := args[:0:0]
	for _, a := range args {
		if strings.HasPrefix(a, "-psn_") {
			continue
		}
		out = append(out, a)
	}
	return out
}
