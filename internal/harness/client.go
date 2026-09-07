package harness

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

// Client talks to an OpenAI-compatible HTTP API:
//
//	GET  /v1/models
//	POST /v1/responses
//	POST /v1/chat/completions
//	POST /v1/completions
//	POST /v1/embeddings
//
// Config.BaseURL includes the /v1 prefix, so request paths below are
// relative to it (e.g. baseURL + "/chat/completions").
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewClient(cfg Config) *Client {
	timeout, err := timeoutDuration(cfg.Timeout)
	if err != nil {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// do is the shared request/decode path. body may be nil for GETs.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("%s %s: unexpected status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: decode response: %w", method, path, err)
	}
	return nil
}

// --- GET /v1/models -------------------------------------------------------

// Models lists the model IDs available on the server.
func (c *Client) Models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, "/models", nil, &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// --- POST /v1/chat/completions ---------------------------------------------

type ChatMessage struct {
	Role    string `json:"role"` // "system" | "user" | "assistant"
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
}

type ChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			ChatMessage
			// Reasoning models (e.g. some Gemma/Qwen builds served by LM
			// Studio) put their chain-of-thought here; content holds the
			// visible answer. Reasoning tokens still count against max_tokens.
			ReasoningContent string `json:"reasoning_content,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Text returns the first choice's message content ("" if none).
func (r *ChatResponse) Text() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// ChatCompletion sends a chat request (the main API the game master will use).
func (c *Client) ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var out ChatResponse
	if err := c.do(ctx, http.MethodPost, "/chat/completions", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- POST /v1/completions ---------------------------------------------------

// CompletionRequest is the legacy text-completion API (prompt in, text out).
type CompletionRequest struct {
	Model       string  `json:"model"`
	Prompt      string  `json:"prompt"`
	Temperature float64 `json:"temperature,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
}

type CompletionResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Text string `json:"text"`
	} `json:"choices"`
}

func (r *CompletionResponse) Text() string {
	if len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Text
}

// Completion sends a legacy completion request.
func (c *Client) Completion(ctx context.Context, req CompletionRequest) (*CompletionResponse, error) {
	var out CompletionResponse
	if err := c.do(ctx, http.MethodPost, "/completions", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- POST /v1/responses -----------------------------------------------------

type ResponsesRequest struct {
	Model           string  `json:"model"`
	Input           string  `json:"input"`
	Instructions    string  `json:"instructions,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"max_output_tokens,omitempty"`
}

type ResponsesResponse struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Output json.RawMessage `json:"output"` // TODO: parse into typed output items
}

// Responses sends a request against the newer Responses API.
func (c *Client) Responses(ctx context.Context, req ResponsesRequest) (*ResponsesResponse, error) {
	var out ResponsesResponse
	if err := c.do(ctx, http.MethodPost, "/responses", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- POST /v1/embeddings ----------------------------------------------------

type EmbeddingsRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type EmbeddingsResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

// Embeddings embeds the inputs (e.g. for world-state retrieval later).
func (c *Client) Embeddings(ctx context.Context, req EmbeddingsRequest) (*EmbeddingsResponse, error) {
	var out EmbeddingsResponse
	if err := c.do(ctx, http.MethodPost, "/embeddings", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
