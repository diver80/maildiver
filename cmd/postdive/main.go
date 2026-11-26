// Command postdive is the standalone native PostDive desktop application
// (IMAP/SMTP + SQLite + Contacts + Google Calendar/Contacts & iCal + AI Assistant).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/jobs"
	"github.com/diver80/postdive/internal/mailengine"
	"github.com/diver80/postdive/internal/mcp"
	"github.com/diver80/postdive/internal/nativeui"
	"github.com/diver80/postdive/internal/triage"
)

var version = "0.7.48-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() error {
	config.LoadEnvFile()
	dropStaleTempDir()

	fs := flag.NewFlagSet("postdive", flag.ExitOnError)
	minimized := fs.Bool("minimized", false, "Nur als Menüleisten-/Tray-Status-Widget starten")
	noTray := fs.Bool("no-tray", false, "Menüleisten-/Tray-Status-Widget deaktivieren")
	showVersion := fs.Bool("version", false, "Version ausgeben")
	mcpMode := fs.Bool("mcp", false, "Als lokaler MCP-Server über stdio laufen (Standard: nur lesend)")
	mcpAllowDrafts := fs.Bool("allow-drafts", false, "Im --mcp-Modus create_draft erlauben (speichert nur Entwürfe, sendet nie)")
	mcpAllowWrites := fs.Bool("allow-writes", false, "Im --mcp-Modus Entwürfe, Termine und Gelesen/Verschieben/Stern erlauben (nie Senden/Löschen)")
	_ = fs.Parse(filterLaunchArgs(os.Args[1:]))

	if *showVersion {
		fmt.Println("postdive", version)
		return nil
	}

	if *mcpMode {
		return runMCP(context.Background(), *mcpAllowDrafts, *mcpAllowWrites)
	}

	initialMailto := ""
	for _, arg := range fs.Args() {
		if len(arg) >= 7 && strings.EqualFold(arg[:7], "mailto:") {
			initialMailto = arg
			break
		}
	}

	// Passwords on Android: sealed with a key of the Android keystore.
	nativeui.SetupPlatformSecrets()
	nativeui.RepairTransferredAccounts(config.Load, config.Save)

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
		return fmt.Errorf("Fehler beim Starten der PostDive-Engine: %w", err)
	}
	// On Android/iOS, keep persistent IMAP IDLE workers paused until the Activity is active
	// and online: headless WorkManager wakes use SyncInboxes directly and must not leave IDLE
	// goroutines hitting Android's background network firewall ("no such host").
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		eng.SetWorkersPaused(true)
	}
	eng.Start(ctx)
	defer func() { _ = eng.Close() }()

	var runner *jobs.Runner
	if store != nil {
		runner = jobs.New(ctx, eng, eng.Events(), store, config.Load)
		// "Bei neuer Mail automatisch scannen": the runner checks the setting on every event.
		runner.FollowNewMail(eng.Events())
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
		Version:       version,
		StartHidden:   *minimized,
		DisableTray:   *noTray,
		InitialMailto: initialMailto,
	}

	if err := nativeui.Run(ctx, uiOpts); err != nil {
		return fmt.Errorf("Fehler: %w", err)
	}
	return nil
}

// runMCP starts PostDive as a local stdio Model Context Protocol (MCP) server without launching
// the GUI or persistent IMAP IDLE loops (so it coexists cleanly with a running desktop app via SQLite WAL).
func runMCP(parent context.Context, allowDrafts, allowWrites bool) error {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := triage.OpenStore()
	if err != nil {
		fmt.Fprintf(os.Stderr, "postdive --mcp: triage store warning: %v\n", err)
	}

	eng, err := mailengine.New(mailengine.Options{
		ConfigLoader: config.Load,
		ConfigSaver:  config.Save,
		// What an agent does over MCP shows as such in the activity log.
		ActivitySource: mailengine.ActivityByMCP,
	})
	if err != nil {
		return fmt.Errorf("postdive --mcp: engine init failed: %w", err)
	}
	defer func() { _ = eng.Close() }()

	bc := backend.New(backend.Options{
		Engine:     eng,
		Events:     eng.Events(),
		Store:      store,
		LoadConfig: config.Load,
		Version:    version,
	})
	srv := mcp.New(mcp.Options{
		Backend:     bc,
		Store:       store,
		LoadConfig:  config.Load,
		Version:     version,
		AllowDrafts: allowDrafts,
		AllowWrites: allowWrites,
	})
	// A read of stdin cannot be interrupted, so Serve runs beside the wait for a signal: the
	// server ends when the client closes stdin or on SIGINT/SIGTERM, whichever comes first.
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, os.Stdin, os.Stdout) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return nil
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

// dropStaleTempDir forgets a TMPDIR that no longer exists. Started from another tool's session
// (an IDE, an agent), the app inherits that session's temp directory, which the tool may delete
// while the app keeps running; the system default is used instead. Child processes (AI CLIs)
// inherit the corrected environment.
func dropStaleTempDir() {
	dir := os.Getenv("TMPDIR")
	if dir == "" {
		return
	}
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return
	}
	_ = os.Unsetenv("TMPDIR")
}
