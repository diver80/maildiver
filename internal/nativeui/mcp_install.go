package nativeui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type mcpClientTarget struct {
	ID         string
	Name       string
	ConfigPath string
}

func mcpArgsForMode(mode int) []string {
	switch mode {
	case 1:
		return []string{"--mcp", "--allow-drafts"}
	case 2:
		return []string{"--mcp", "--allow-writes"}
	default:
		return []string{"--mcp"}
	}
}

func postdiveMCPCommand() string {
	exe, err := os.Executable()
	if err == nil && strings.TrimSpace(exe) != "" {
		return exe
	}
	return "postdive"
}

func mcpClientTargets(home, goos string) []mcpClientTarget {
	useEnv := strings.TrimSpace(home) == ""
	if useEnv {
		home, _ = os.UserHomeDir()
	}
	home = strings.TrimSpace(home)
	if home == "" || !filepath.IsAbs(home) {
		return nil
	}
	var claudePath string
	switch goos {
	case "darwin":
		claudePath = filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		appData := ""
		if useEnv {
			appData = strings.TrimSpace(os.Getenv("APPDATA"))
		}
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		claudePath = filepath.Join(appData, "Claude", "claude_desktop_config.json")
	default:
		cfgHome := ""
		if useEnv {
			cfgHome = strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		}
		if cfgHome == "" {
			cfgHome = filepath.Join(home, ".config")
		}
		claudePath = filepath.Join(cfgHome, "Claude", "claude_desktop_config.json")
	}
	return []mcpClientTarget{
		{
			ID:         "claude",
			Name:       "Claude Desktop",
			ConfigPath: claudePath,
		},
		{
			ID:         "cursor",
			Name:       "Cursor",
			ConfigPath: filepath.Join(home, ".cursor", "mcp.json"),
		},
		{
			ID:         "agy",
			Name:       "Antigravity",
			ConfigPath: filepath.Join(home, ".gemini", "antigravity", "mcp_config.json"),
		},
	}
}

func mcpCurrentClientTargets() []mcpClientTarget {
	home, _ := os.UserHomeDir()
	return mcpClientTargets(home, runtime.GOOS)
}

func isMCPServerInstalled(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return false
	}
	servers, ok := root["mcpServers"].(map[string]any)
	if !ok {
		return false
	}
	_, exists := servers["postdive"]
	return exists
}

// installMCPServerConfig merges the "postdive" MCP stdio server entry into a client JSON config file
// without discarding unrelated keys or servers. Existing files receive a .postdive.bak backup first.
func installMCPServerConfig(path, command string, args []string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("empty config path")
	}
	if strings.TrimSpace(command) == "" {
		command = "postdive"
	}
	argsCopy := append([]string(nil), args...)
	if len(argsCopy) == 0 {
		argsCopy = []string{"--mcp"}
	}

	root := map[string]any{}
	var origRaw []byte
	var hasOrig bool
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != "" {
		path = resolved
	}
	if raw, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(raw))) > 0 {
			if jerr := json.Unmarshal(raw, &root); jerr != nil {
				return fmt.Errorf("invalid JSON in %s: %w", filepath.Base(path), jerr)
			}
			if root == nil {
				root = map[string]any{}
			}
		}
		origRaw = raw
		hasOrig = true
	} else if !os.IsNotExist(err) {
		return err
	}

	servers := map[string]any{}
	if existing, ok := root["mcpServers"]; ok && existing != nil {
		m, ok := existing.(map[string]any)
		if !ok {
			return fmt.Errorf("unexpected mcpServers type in %s", filepath.Base(path))
		}
		servers = m
	}
	if hasOrig {
		bakPath := path + ".postdive.bak"
		if _, statErr := os.Stat(bakPath); os.IsNotExist(statErr) {
			if berr := os.WriteFile(bakPath, origRaw, 0o600); berr != nil {
				return fmt.Errorf("backup %s: %w", filepath.Base(bakPath), berr)
			}
		}
	}
	servers["postdive"] = map[string]any{
		"command": command,
		"args":    argsCopy,
	}
	root["mcpServers"] = servers

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".postdive-mcp-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
