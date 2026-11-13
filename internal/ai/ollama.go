package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Ollama struct {
	model    string
	endpoint string
	client   *http.Client
}

func NewOllama(model, endpoint string) *Ollama {
	if strings.TrimSpace(model) == "" {
		model = "qwen3:4b"
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "http://127.0.0.1:11434"
	}
	return &Ollama{
		model:    model,
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (o *Ollama) Name() string      { return "ollama" }
func (o *Ollama) ModelName() string { return o.model }

func (o *Ollama) Complete(ctx context.Context, systemPrompt, userPrompt string, expectJSON bool) (string, error) {
	reqBody := map[string]any{
		"model":  o.model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": userPrompt},
		},
	}
	if expectJSON {
		reqBody["format"] = "json"
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint+"/api/chat", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	return parsed.Message.Content, nil
}
