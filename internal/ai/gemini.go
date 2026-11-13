package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type GeminiAPI struct {
	model    string
	endpoint string
	client   *http.Client
}

func NewGeminiAPI(model, endpoint string) *GeminiAPI {
	if strings.TrimSpace(model) == "" {
		model = "gemini-4-hydrogen"
	}
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "https://generativelanguage.googleapis.com"
	}
	return &GeminiAPI{
		model:    model,
		endpoint: strings.TrimRight(endpoint, "/"),
		client:   &http.Client{Timeout: 45 * time.Second},
	}
}

func (g *GeminiAPI) Name() string      { return "gemini-api" }
func (g *GeminiAPI) ModelName() string { return g.model }

func (g *GeminiAPI) Complete(ctx context.Context, systemPrompt, userPrompt string, expectJSON bool) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("GOOGLE_API_KEY")
	}
	if apiKey == "" {
		return "", fmt.Errorf("GEMINI_API_KEY oder GOOGLE_API_KEY ist nicht gesetzt")
	}

	genCfg := map[string]any{
		"temperature": 0.2,
	}
	if expectJSON {
		genCfg["responseMimeType"] = "application/json"
	}

	reqBody := map[string]any{
		"systemInstruction": map[string]any{
			"parts": []map[string]string{{"text": systemPrompt}},
		},
		"contents": []map[string]any{
			{
				"role":  "user",
				"parts": []map[string]string{{"text": userPrompt}},
			},
		},
		"generationConfig": genCfg,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", g.endpoint, g.model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini api HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("gemini api returned empty candidates")
	}
	var out strings.Builder
	for _, p := range parsed.Candidates[0].Content.Parts {
		out.WriteString(p.Text)
	}
	return out.String(), nil
}
