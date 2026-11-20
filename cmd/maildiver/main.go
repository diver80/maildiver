// Command maildiver is the standalone native MailDiver desktop application
// (IMAP/SMTP + SQLite + Contacts + Google Calendar/Contacts & iCal + AI Assistant).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/config"
	"github.com/diver80/maildiver/internal/jobs"
	"github.com/diver80/maildiver/internal/mailengine"
	"github.com/diver80/maildiver/internal/nativeui"
	"github.com/diver80/maildiver/internal/triage"
)

var version = "0.5.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config.LoadEnvFile()

	fs := flag.NewFlagSet("maildiver", flag.ExitOnError)
	minimized := fs.Bool("minimized", false, "Nur als Menüleisten-/Tray-Status-Widget starten")
	noTray := fs.Bool("no-tray", false, "Menüleisten-/Tray-Status-Widget deaktivieren")
	showVersion := fs.Bool("version", false, "Version ausgeben")
	_ = fs.Parse(filterLaunchArgs(os.Args[1:]))

	if *showVersion {
		fmt.Println("maildiver", version)
		return nil
	}

	initialMailto := ""
	for _, arg := range fs.Args() {
		if len(arg) >= 7 && strings.EqualFold(arg[:7], "mailto:") {
			initialMailto = arg
			break
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := triage.OpenStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warnung: Triage-Store nicht geöffnet: %v\n", err)
	}

	eng, err := mailengine.New(mailengine.Options{
		ConfigLoader: config.Load,
		ConfigSaver:  config.Save,
	})
	if err != nil {
		return fmt.Errorf("Fehler beim Starten der MailDiver-Engine: %w", err)
	}
	eng.Start(ctx)
	defer func() { _ = eng.Close() }()

	var runner *jobs.Runner
	if store != nil {
		runner = jobs.New(ctx, eng, eng.Events(), store, config.Load)
	}

	// The app talks to the engine, the AI layer and the jobs directly, in this process.
	uiOpts := nativeui.Options{
		Store: store,
		Backend: backend.New(backend.Options{
			Engine:     eng,
			Events:     eng.Events(),
			Store:      store,
			Jobs:       runner,
			LoadConfig: config.Load,
			Version:    version,
		}),
		Direct:        eng,
		StartHidden:   *minimized,
		DisableTray:   *noTray,
		InitialMailto: initialMailto,
	}

	if err := nativeui.Run(ctx, uiOpts); err != nil {
		return fmt.Errorf("Fehler: %w", err)
	}
	return nil
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
