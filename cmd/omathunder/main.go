package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/diver80/omathunder/internal/ai"
	"github.com/diver80/omathunder/internal/bridge"
	"github.com/diver80/omathunder/internal/config"
	"github.com/diver80/omathunder/internal/native"
	"github.com/diver80/omathunder/internal/theme"
	"github.com/diver80/omathunder/internal/triage"
)

var version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	native.Version = version
	native.LoadEnvFile()
	cmd := os.Args[1]
	// When Thunderbird launches the Native Messaging Host directly on some platforms,
	// it passes the manifest path as the sole argument.
	if strings.HasSuffix(cmd, ".json") || cmd == "host" {
		if err := runHostWithBridge(context.Background()); err != nil {
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
		// Homebrew: register the opt/ path so the host keeps working after `brew upgrade`.
		exe = native.StableBinaryPath(exe)
		paths, err := native.InstallHost(exe)
		if err != nil {
			fatal(err)
		}
		// Write defaults only for a fresh install; never replace a config that failed to parse.
		if cfg, err := config.Load(); err == nil {
			_ = config.Save(cfg)
		} else {
			fmt.Fprintf(os.Stderr, "Hinweis: config.json nicht lesbar, unverändert gelassen: %v\n", err)
		}
		fmt.Println("omathunder Native Messaging Host registriert:")
		for _, p := range paths {
			fmt.Println("  -", p)
		}
		fmt.Println("Thunderbird-Profil und prefs.js blieben vollständig unangetastet.")
		if xpi := native.ExtensionPackagePath(exe); xpi != "" {
			fmt.Println("\nAdd-on für Thunderbird (Add-ons und Themes → Zahnrad → Add-on aus Datei installieren):")
			fmt.Println("  ", xpi)
		}

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

	case "bridge":
		handleBridgeCLI(os.Args[2:])

	case "ui":
		handleUICLI(os.Args[2:])

	default:
		printUsage()
		os.Exit(1)
	}
}

func runHostWithBridge(ctx context.Context) error {
	return native.RunHostWithOptions(ctx, os.Stdin, os.Stdout, native.HostOptions{
		OnReady: func(hostCtx context.Context, h *native.Handler) (func(), error) {
			// The bridge is opt-in: the supervisor only opens the localhost port while
			// bridge.enabled is true and follows changes to config.json at runtime.
			sup := bridge.StartSupervisor(hostCtx, bridge.Options{
				RPC:    h.RPC(),
				Events: h.Events(),
				Store:  h.Store(),
				Jobs:   h.Jobs(),
			}, 2*time.Second, func(format string, args ...any) {
				fmt.Fprintf(os.Stderr, format+"\n", args...)
			})
			h.SetConfigChangedHook(sup.Reconcile)
			return sup.Close, nil
		},
	})
}

func handleBridgeCLI(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}
	discPath, err := config.BridgeInfoPath()
	if err != nil {
		fatal(err)
	}
	switch sub {
	case "path":
		fmt.Println(discPath)
		return
	case "enable", "disable":
		cfg, err := config.Load()
		if err != nil {
			fatal(fmt.Errorf("config.json nicht lesbar, nichts geändert: %w", err))
		}
		cfg.Bridge.Enabled = sub == "enable"
		if err := config.Save(cfg); err != nil {
			fatal(err)
		}
		if cfg.Bridge.Enabled {
			fmt.Println("Bridge aktiviert. Sie startet, sobald das OmaThunder-Add-on in Thunderbird verbunden ist (ca. 2 s).")
		} else {
			fmt.Println("Bridge deaktiviert. Der lokale Port wird innerhalb von ca. 2 s geschlossen.")
		}
		return
	case "status":
	default:
		fatal(fmt.Errorf("usage: omathunder bridge [status|path|enable|disable]"))
	}
	info, err := bridge.ReadDiscoveryFile(discPath)
	if err != nil {
		cfg, _ := config.Load()
		out, _ := json.MarshalIndent(map[string]any{
			"running":    false,
			"enabled":    cfg.Bridge.Enabled,
			"bridgeFile": discPath,
			"error":      err.Error(),
		}, "", "  ")
		fmt.Println(string(out))
		return
	}
	client := &http.Client{Timeout: 3 * time.Second}
	req, _ := http.NewRequest(http.MethodGet, info.URL+"/v1/health", nil)
	req.Header.Set("Authorization", "Bearer "+info.Token)
	resp, err := client.Do(req)
	if err != nil {
		out, _ := json.MarshalIndent(map[string]any{
			"running":    false,
			"bridgeFile": discPath,
			"url":        info.URL,
			"pid":        info.PID,
			"error":      err.Error(),
		}, "", "  ")
		fmt.Println(string(out))
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	var health map[string]any
	_ = json.Unmarshal(body, &health)
	out, _ := json.MarshalIndent(map[string]any{
		"running":    resp.StatusCode == http.StatusOK,
		"bridgeFile": discPath,
		"url":        info.URL,
		"port":       info.Port,
		"pid":        info.PID,
		"health":     health["result"],
	}, "", "  ")
	fmt.Println(string(out))
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
		cfg, err := config.Load()
		if err != nil {
			fatal(fmt.Errorf("config.json nicht lesbar, nichts geändert: %w", err))
		}
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

// handleUICLI starts the native OmaThunder app. The app is a separate program (omathunder-ui,
// on macOS OmaThunder.app) so the Native Messaging host stays small and has no GUI dependencies.
func handleUICLI(args []string) {
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	target, viaOpen := findUIApp(exe, runtime.GOOS, fileExists, exec.LookPath)
	if target == "" {
		fatal(fmt.Errorf("OmaThunder-App nicht gefunden (omathunder-ui bzw. OmaThunder.app). Installiere sie mit ./install.sh oder baue sie mit ./build.sh"))
	}
	var cmd *exec.Cmd
	if viaOpen {
		openArgs := []string{"-a", target}
		if len(args) > 0 {
			openArgs = append(openArgs, "--args")
			openArgs = append(openArgs, args...)
		}
		cmd = exec.Command("open", openArgs...)
	} else {
		cmd = exec.Command(target, args...)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		fatal(err)
	}
}

// findUIApp locates the native app: a sibling omathunder-ui binary, then (macOS) OmaThunder.app
// in ~/Applications or /Applications (started via `open`), then omathunder-ui on PATH.
func findUIApp(exe, goos string, exists func(string) bool, lookPath func(string) (string, error)) (path string, viaOpen bool) {
	name := "omathunder-ui"
	if goos == "windows" {
		name += ".exe"
	}
	if exe != "" {
		if sib := filepath.Join(filepath.Dir(exe), name); exists(sib) {
			return sib, false
		}
	}
	if goos == "darwin" {
		var dirs []string
		if home, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(home, "Applications"))
		}
		dirs = append(dirs, "/Applications")
		for _, d := range dirs {
			if app := filepath.Join(d, "OmaThunder.app"); exists(app) {
				return app, true
			}
		}
	}
	if p, err := lookPath(name); err == nil {
		return p, false
	}
	return "", false
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func printUsage() {
	fmt.Print(`omathunder — Live Omarchy Theme & 3-Stufen-KI-Companion für Thunderbird

Verwendung:
  omathunder ui [--minimized] [--no-tray]  Native OmaThunder-App starten (omathunder-ui / OmaThunder.app)
  omathunder setup                         Native Messaging Host sicher registrieren
  omathunder uninstall                     Native Host & omathunder-Daten sauber entfernen
  omathunder doctor                        System-, Omarchy- und KI-Status prüfen
  omathunder bridge [status|path|enable|disable]  Lokale REST/SSE-Bridge prüfen bzw. ein-/ausschalten
  omathunder theme [list|set]              Themes auflisten, umschalten oder als JSON ausgeben
  omathunder classify                      Test-Klassifikation (Stage 0 + Stage 1) per CLI
  omathunder bar-summary                   Kompakte Triage-Zahlen für Omarchy-Bar ausgeben
  omathunder host                          Thunderbird Native Messaging Loop (stdio)
`)
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Fehler: %v\n", err)
	os.Exit(1)
}
