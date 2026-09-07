package harness

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer answers all five OpenAI-compatible endpoints with minimal
// valid payloads so the client can be tested without a real LLM server.
func stubServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data":   []map[string]any{{"id": "stub-model"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var req map[string]any
			json.NewDecoder(r.Body).Decode(&req)
			if req["model"] == "" {
				t.Errorf("chat request missing model")
			}
			json.NewEncoder(w).Encode(map[string]any{
				"id": "chat-1",
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]string{
						"role": "assistant", "content": "the orc attacks",
					},
					"finish_reason": "stop",
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/completions":
			json.NewEncoder(w).Encode(map[string]any{
				"id":      "cmpl-1",
				"choices": []map[string]any{{"text": "you enter the crypt"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/responses":
			json.NewEncoder(w).Encode(map[string]any{
				"id": "resp-1", "status": "completed",
				"output": []map[string]any{{"type": "message"}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/embeddings":
			json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"index": 0, "embedding": []float64{0.1, 0.2, 0.3}},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return NewClient(Config{BaseURL: baseURL + "/v1", Timeout: "5s", Model: "stub-model"})
}

func TestClientEndpoints(t *testing.T) {
	ctx := context.Background()
	c := newTestClient(t, stubServer(t).URL)

	models, err := c.Models(ctx)
	if err != nil || len(models) != 1 || models[0] != "stub-model" {
		t.Fatalf("Models: models=%v err=%v", models, err)
	}

	chat, err := c.ChatCompletion(ctx, ChatRequest{
		Model:    "stub-model",
		Messages: []ChatMessage{{Role: "user", Content: "what happens?"}},
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if got := chat.Text(); got != "the orc attacks" {
		t.Errorf("chat text = %q", got)
	}

	cmpl, err := c.Completion(ctx, CompletionRequest{Model: "stub-model", Prompt: "you"})
	if err != nil || cmpl.Text() != "you enter the crypt" {
		t.Fatalf("Completion: text=%q err=%v", cmpl.Text(), err)
	}

	resp, err := c.Responses(ctx, ResponsesRequest{Model: "stub-model", Input: "hi"})
	if err != nil || resp.ID != "resp-1" || resp.Status != "completed" {
		t.Fatalf("Responses: %+v err=%v", resp, err)
	}

	emb, err := c.Embeddings(ctx, EmbeddingsRequest{Model: "stub-model", Input: []string{"tavern"}})
	if err != nil || len(emb.Data) != 1 || len(emb.Data[0].Embedding) != 3 {
		t.Fatalf("Embeddings: %+v err=%v", emb, err)
	}
}

func TestClientServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL)
	_, err := c.Models(context.Background())
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("expected 404 error with body, got %v", err)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig("../../configs/harness.yaml")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.BaseURL == "" || !cfg.Enabled {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}
