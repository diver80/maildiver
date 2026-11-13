package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fhess/omathunder/internal/ai"
	"github.com/fhess/omathunder/internal/config"
	"github.com/fhess/omathunder/internal/native"
	"github.com/fhess/omathunder/internal/theme"
	"github.com/fhess/omathunder/internal/triage"
)

var version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	// When Thunderbird launches the Native Messaging Host directly on some platforms,
	// it passes the manifest path as the sole argument.
	if strings.HasSuffix(cmd, ".json") || cmd == "host" {
		if err := native.RunHost(context.Background(), os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "omathunder host error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	switch cmd {
	case "version", "--version", "-v":
		fmt.Println("omathunder", version)

	case "setup":
		exe, err := os.Executable()
		if err != nil {
			fatal(err)
		}
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		paths, err := native.InstallHost(exe)
		if err != nil {
			fatal(err)
		}
		cfg, _ := config.Load()
		_ = config.Save(cfg)
		fmt.Println("omathunder Native Messaging Host registriert:")
		for _, p := range paths {
			fmt.Println("  -", p)
		}
		fmt.Println("Thunderbird-Profil und prefs.js blieben vollständig unangetastet.")

	case "uninstall":
		removed, err := native.UninstallHost()
		if err != nil {
			fatal(err)
		}
		if stateDir, err := config.StateDir(); err == nil {
			if err := os.RemoveAll(stateDir); err == nil {
				removed = append(removed, stateDir)
			}
		}
		if cfgDir, err := config.ConfigDir(); err == nil {
			if err := os.RemoveAll(cfgDir); err == nil {
				removed = append(removed, cfgDir)
			}
		}
		fmt.Println("omathunder sauber entfernt (Thunderbird unberührt):")
		for _, p := range removed {
			fmt.Println("  - entfernt:", p)
		}

	case "doctor":
		out, _ := json.MarshalIndent(native.BuildDoctorReport(), "", "  ")
		fmt.Println(string(out))

	case "theme":
		handleThemeCLI(os.Args[2:])

	case "classify":
		fs := flag.NewFlagSet("classify", flag.ExitOnError)
		subject := fs.String("subject", "", "E-Mail-Betreff")
		from := fs.String("from", "", "Absender")
		body := fs.String("body", "", "Nachrichtentext")
		_ = fs.Parse(os.Args[2:])

		cfg, _ := config.Load()
		res, err := ai.Classify(context.Background(), cfg.AI, triage.MessageInput{
			ID:       "cli-msg",
			Subject:  *subject,
			From:     *from,
			BodyText: *body,
			Unread:   true,
		}, cfg.Rules)
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		if err != nil {
			fmt.Fprintf(os.Stderr, "hinweis (fallback aktiv): %v\n", err)
		}

	case "bar-summary":
		stDir, err := config.StateDir()
		if err != nil {
			fatal(err)
		}
		store := triage.NewStoreAt(stDir)
		st, err := store.Load()
		if err != nil {
			fatal(err)
		}
		out, _ := json.MarshalIndent(triage.ComputeBarSummary(st), "", "  ")
		fmt.Println(string(out))

	default:
		printUsage()
		os.Exit(1)
	}
}

func handleThemeCLI(args []string) {
	if len(args) == 0 || args[0] == "current" {
		cfg, _ := config.Load()
		pal, src := theme.ResolveActive(cfg.Theme)
		out, _ := json.MarshalIndent(theme.BuildPayload(pal, src), "", "  ")
		fmt.Println(string(out))
		return
	}
	switch args[0] {
	case "list":
		for _, p := range theme.ListPresets() {
			mode := "dark"
			if p.IsLight {
				mode = "light"
			}
			fmt.Printf("%-18s %-20s [%s] bg=%s accent=%s\n", p.Name, p.Label, mode, p.Background, p.Accent)
		}
	case "set":
		if len(args) < 2 {
			fatal(fmt.Errorf("usage: omathunder theme set <preset|auto|omarchy>"))
		}
		cfg, _ := config.Load()
		target := args[1]
		if target == "auto" || target == "omarchy" {
			cfg.Theme.Mode = target
		} else {
			if _, ok := theme.GetPreset(target); !ok {
				fatal(fmt.Errorf("unbekanntes Theme %q (nutze `omathunder theme list`)", target))
			}
			cfg.Theme.Mode = "preset"
			cfg.Theme.Preset = target
		}
		if err := config.Save(cfg); err != nil {
			fatal(err)
		}
		pal, src := theme.ResolveActive(cfg.Theme)
		fmt.Printf("Aktives Theme: %s (%s)\n", pal.Label, src)
	default:
		fatal(fmt.Errorf("unbekannter theme-Unterbefehl: %s", args[0]))
	}
}

func printUsage() {
	fmt.Print(`omathunder — Live Omarchy Theme & 3-Stufen-KI-Companion für Thunderbird

Verwendung:
  omathunder setup              Native Messaging Host sicher registrieren
  omathunder uninstall          Native Host & omathunder-Daten sauber entfernen
  omathunder doctor             System-, Omarchy- und KI-Status prüfen
  omathunder theme [list|set]   Themes auflisten, umschalten oder als JSON ausgeben
  omathunder classify           Test-Klassifikation (Stage 0 + Stage 1) per CLI
  omathunder bar-summary        Kompakte Triage-Zahlen für Omarchy-Bar ausgeben
  omathunder host               Thunderbird Native Messaging Loop (stdio)
`)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
	os.Exit(1)
}
