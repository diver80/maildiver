package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// agyDeniedTools are all tools the Antigravity CLI (1.5) offers. agy has no switch that turns
// tools off and no wildcard deny rule, so PostDive denies each one by name: a prompt injection in
// a mail must not read files, open URLs or run commands. A tool added by a later agy release is
// not on this list, which is why agy stays behind the "unrestricted CLIs" opt-in.
var agyDeniedTools = []string{
	"ask_custom_permission", "ask_permission", "ask_question",
	"browser_click_element", "browser_drag_pixel_to_pixel", "browser_get_dom", "browser_get_network_request",
	"browser_input", "browser_list_network_requests", "browser_mouse_down", "browser_mouse_up",
	"browser_move_mouse", "browser_press_key", "browser_refresh_page", "browser_resize_window",
	"browser_scroll", "browser_scroll_dom", "browser_select_option", "browser_subagent",
	"call_mcp_tool", "capture_browser_console_logs", "capture_browser_screenshot", "click_browser_pixel",
	"command_status", "define_subagent", "delete_knowledge", "edit_file", "execute_browser_javascript", "find_by_name",
	"generate_image", "grep_search", "invoke_subagent", "list_browser_pages", "list_dir",
	"list_permissions", "list_plugin_accounts", "list_resources", "manage_inbox", "manage_subagents",
	"manage_task", "multi_replace_file_content", "notebook_edit", "notebook_execution", "open_browser_url",
	"read_browser_page", "read_file", "read_resource", "read_url_content", "replace_file_content",
	"run_command", "run_workflow", "schedule", "search_marketplace", "search_web", "sed_file",
	"send_command_input", "send_message", "swarm", "view_file", "write_file", "write_to_file",
}

// agySettings is the session-only settings JSON (never saved by agy).
func agySettings() string {
	deny := make([]string, 0, len(agyDeniedTools)+2)
	for _, t := range agyDeniedTools {
		deny = append(deny, t+"(*)")
	}
	deny = append(deny, "command(*)", "mcp(*)")
	raw, _ := json.Marshal(map[string]any{
		"permissions":             map[string]any{"deny": deny},
		"allowNonWorkspaceAccess": false,
	})
	return string(raw)
}

// AgyCLI runs Google's Antigravity CLI (successor of the Gemini CLI) in print mode. The prompt
// goes in as one stream-json message on stdin, so no mail content shows up in the process list.
type AgyCLI struct {
	model  string
	binary string
}

func NewAgyCLI(model string) *AgyCLI { return &AgyCLI{model: model, binary: "agy"} }

func (c *AgyCLI) Name() string { return "agy" }
func (c *AgyCLI) ModelName() string {
	if c.model == "" {
		return "default"
	}
	return c.model
}

// agyArgs are the process arguments: constant flags, the constant settings and the model only.
func (c *AgyCLI) agyArgs() []string {
	args := []string{
		"--sandbox",
		"--settings", agySettings(),
		"--disable-slash-commands",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
	}
	if m := strings.TrimSpace(c.model); m != "" {
		args = append(args, "--model", m)
	}
	// -p takes the next argument as its prompt; "-p=" keeps it empty so the stdin message counts.
	return append(args, "-p=")
}

func (c *AgyCLI) Complete(ctx context.Context, systemPrompt, userPrompt string, _ bool) (string, error) {
	name := c.binary
	if name == "" {
		name = "agy"
	}
	bin, err := findCLI(name)
	if err != nil {
		return "", fmt.Errorf("agy CLI nicht im PATH gefunden / not found in PATH")
	}
	msg, err := json.Marshal(map[string]any{
		"event":   "user",
		"message": map[string]string{"content": cliSystemPrompt + "\n\n" + cliInput(systemPrompt, userPrompt)},
	})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, c.agyArgs()...)
	cmd.Dir = cliWorkDir()
	cmd.Env = sanitizedCLIEnv()
	cmd.Stdin = bytes.NewReader(append(msg, '\n'))
	stdout := &limitedBuffer{max: cliMaxStdoutBytes}
	stderr := &limitedBuffer{max: cliMaxStderrBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	out, parseErr := parseAgyResult(stdout.Bytes())
	if parseErr == nil {
		return out, nil
	}
	if runErr != nil {
		return "", fmt.Errorf("agy CLI failed: %w (%s)", runErr, truncateForError(stderr.String()))
	}
	return "", parseErr
}

// parseAgyResult takes the answer from agy's final "result" event.
func parseAgyResult(stream []byte) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(stream))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Event  string `json:"event"`
			Result *struct {
				Status   string `json:"status"`
				Response string `json:"response"`
				Error    string `json:"error"`
			} `json:"result"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Event != "result" || ev.Result == nil {
			continue
		}
		if ev.Result.Status != "SUCCESS" {
			msg := ev.Result.Error
			if msg == "" {
				msg = ev.Result.Status
			}
			return "", fmt.Errorf("agy CLI error: %s", truncateForError(msg))
		}
		return strings.TrimSpace(ev.Result.Response), nil
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("agy CLI: stream read error: %w", err)
	}
	return "", errors.New("agy CLI: no result in output")
}
