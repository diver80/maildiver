package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type CLICompleter struct {
	name     string
	binary   string
	model    string
	buildArg func(prompt, model string) []string
}

func NewCLICompleter(name, binary, model string, buildArg func(prompt, model string) []string) *CLICompleter {
	return &CLICompleter{name: name, binary: binary, model: model, buildArg: buildArg}
}

func (c *CLICompleter) Name() string { return c.name }
func (c *CLICompleter) ModelName() string {
	if c.model == "" {
		return "default"
	}
	return c.model
}

func (c *CLICompleter) Complete(ctx context.Context, systemPrompt, userPrompt string, _ bool) (string, error) {
	if _, err := exec.LookPath(c.binary); err != nil {
		return "", fmt.Errorf("%s CLI nicht im PATH gefunden", c.binary)
	}
	combined := systemPrompt + "\n\n" + userPrompt
	args := c.buildArg(combined, c.model)
	cmd := exec.CommandContext(ctx, c.binary, args...)
	cmd.Dir = os.TempDir()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w (%s)", c.binary, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

type ClaudeCLI struct {
	model string
}

func NewClaudeCLI(model string) *ClaudeCLI {
	return &ClaudeCLI{model: model}
}

func (c *ClaudeCLI) Name() string { return "claude-cli" }
func (c *ClaudeCLI) ModelName() string {
	if c.model == "" {
		return "default"
	}
	return c.model
}

func (c *ClaudeCLI) Complete(ctx context.Context, systemPrompt, userPrompt string, _ bool) (string, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return "", fmt.Errorf("claude CLI nicht im PATH gefunden")
	}
	// The mail text goes in via stdin so it never shows up in the process list.
	args := []string{
		"-p",
		"--system-prompt", systemPrompt,
		"--output-format", "json",
		"--tools", "",
		// No MCP servers or connectors: a prompt injection in a mail must not reach any tool.
		"--strict-mcp-config",
		"--disallowedTools", "mcp__*",
		"--no-session-persistence",
	}
	if strings.TrimSpace(c.model) != "" {
		args = append(args, "--model", c.model)
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = os.TempDir()
	cmd.Stdin = strings.NewReader(userPrompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI failed: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}

	var envelope struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err == nil && envelope.Result != "" {
		if envelope.IsError {
			return "", fmt.Errorf("claude CLI error: %s", envelope.Result)
		}
		return envelope.Result, nil
	}
	return strings.TrimSpace(stdout.String()), nil
}

type ClaudeAPI struct {
	model    string
	endpoint string
	client   *http.Client
}

func NewClaudeAPI(model, endpoint string) *ClaudeAPI {
	if strings.TrimSpace(model) == "" {
		model = "claude-haiku-4-5"
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://api.anthropic.com"
	}
	return &ClaudeAPI{
		model:    model,
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *ClaudeAPI) Name() string      { return "claude-api" }
func (c *ClaudeAPI) ModelName() string { return c.model }

func (c *ClaudeAPI) Complete(ctx context.Context, systemPrompt, userPrompt string, _ bool) (string, error) {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("ANTHROPIC_API_KEY ist nicht gesetzt")
	}
	reqBody := map[string]any{
		"model":      c.model,
		"max_tokens": 2048,
		"system":     systemPrompt,
		"messages": []map[string]string{
			{"role": "user", "content": userPrompt},
		},
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/v1/messages", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("claude api HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	var out strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			out.WriteString(block.Text)
		}
	}
	return out.String(), nil
}
