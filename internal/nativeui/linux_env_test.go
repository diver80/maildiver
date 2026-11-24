package nativeui

import "testing"

func TestConfigureLinuxRuntimeEnv(t *testing.T) {
	t.Run("linux wayland defaults MYGO_GPU=0 and JSC_SIGNAL_FOR_GC=12", func(t *testing.T) {
		env := map[string]string{"WAYLAND_DISPLAY": "wayland-1"}
		configureLinuxRuntimeEnv("linux",
			func(k string) string { return env[k] },
			func(k, v string) error { env[k] = v; return nil },
		)
		if env["MYGO_GPU"] != "0" {
			t.Fatalf("MYGO_GPU = %q, want 0", env["MYGO_GPU"])
		}
		if env["JSC_SIGNAL_FOR_GC"] != "12" {
			t.Fatalf("JSC_SIGNAL_FOR_GC = %q, want 12", env["JSC_SIGNAL_FOR_GC"])
		}
	})

	t.Run("linux wayland respects explicit MYGO_GPU=1 and JSC_SIGNAL_FOR_GC", func(t *testing.T) {
		env := map[string]string{
			"WAYLAND_DISPLAY":   "wayland-1",
			"MYGO_GPU":          "1",
			"JSC_SIGNAL_FOR_GC": "38",
		}
		configureLinuxRuntimeEnv("linux",
			func(k string) string { return env[k] },
			func(k, v string) error { env[k] = v; return nil },
		)
		if env["MYGO_GPU"] != "1" {
			t.Fatalf("MYGO_GPU = %q, want 1", env["MYGO_GPU"])
		}
		if env["JSC_SIGNAL_FOR_GC"] != "38" {
			t.Fatalf("JSC_SIGNAL_FOR_GC = %q, want 38", env["JSC_SIGNAL_FOR_GC"])
		}
	})

	t.Run("linux x11 keeps GPU enabled", func(t *testing.T) {
		env := map[string]string{
			"WAYLAND_DISPLAY": "wayland-1",
			"GDK_BACKEND":     "x11",
		}
		configureLinuxRuntimeEnv("linux",
			func(k string) string { return env[k] },
			func(k, v string) error { env[k] = v; return nil },
		)
		if env["MYGO_GPU"] != "" {
			t.Fatalf("MYGO_GPU = %q, want empty on X11", env["MYGO_GPU"])
		}
		if env["JSC_SIGNAL_FOR_GC"] != "12" {
			t.Fatalf("JSC_SIGNAL_FOR_GC = %q, want 12", env["JSC_SIGNAL_FOR_GC"])
		}
	})

	t.Run("darwin untouched", func(t *testing.T) {
		env := map[string]string{"WAYLAND_DISPLAY": "wayland-1"}
		configureLinuxRuntimeEnv("darwin",
			func(k string) string { return env[k] },
			func(k, v string) error { env[k] = v; return nil },
		)
		if env["MYGO_GPU"] != "" || env["JSC_SIGNAL_FOR_GC"] != "" {
			t.Fatalf("expected no env changes on darwin, got %v", env)
		}
	})
}

func TestFormatShortcutsForOS(t *testing.T) {
	cases := []struct {
		in    string
		linux string
	}{
		{"Ask your AI Assistant (⌘Enter to send)…", "Ask your AI Assistant (Ctrl+Enter to send)…"},
		{"Press ⌘↩ again to send", "Press Ctrl+Enter again to send"},
		{"Undo (⌘Z) / Redo (⌘⇧Z)", "Undo (Ctrl+Z) / Redo (Ctrl+Shift+Z)"},
		{"h  ·  ⌘/", "h  ·  Ctrl+/"},
		{"⌘/shift-Klick", "Ctrl/Shift-Klick"},
		{"⌘-Klick / Shift-Klick", "Ctrl-Klick / Shift-Klick"},
	}
	for _, tc := range cases {
		if got := formatShortcutsForOS(tc.in, "darwin"); got != tc.in {
			t.Errorf("darwin(%q) = %q, want %q", tc.in, got, tc.in)
		}
		if got := formatShortcutsForOS(tc.in, "linux"); got != tc.linux {
			t.Errorf("linux(%q) = %q, want %q", tc.in, got, tc.linux)
		}
	}
}
