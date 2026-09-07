package harness

import (
	"context"
	"os"
	"testing"
	"time"
)

// Integration test against a real OpenAI-compatible server (e.g. LM Studio).
// Skipped unless HARNESS_INTEGRATION=1:
//
//	HARNESS_INTEGRATION=1 go test ./internal/harness -run Integration -v
//
// Uses configs/harness.yaml (or $HARNESS_CONFIG) like the real server does.
func TestIntegrationRealEndpoint(t *testing.T) {
	if os.Getenv("HARNESS_INTEGRATION") != "1" {
		t.Skip("set HARNESS_INTEGRATION=1 to run against a real LLM server")
	}
	path := ConfigPath()
	if _, err := os.Stat(path); err != nil {
		// go test runs in the package dir; try the repo root
		path = "../../configs/harness.yaml"
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig(%s): %v", path, err)
	}
	c := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	models, err := c.Models(ctx)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	t.Logf("models: %v", models)

	chat, err := c.ChatCompletion(ctx, ChatRequest{
		Model: cfg.Model,
		Messages: []ChatMessage{
			{Role: "system", Content: cfg.SystemPrompt},
			{Role: "user", Content: "In one sentence, describe the tavern the players wake up in."},
		},
		Temperature: cfg.Temperature,
		MaxTokens:   400,
	})
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	if r := chat.Choices[0].Message.ReasoningContent; r != "" {
		t.Logf("reasoning: %q", r)
	}
	t.Logf("model=%s reply=%q finish=%s", chat.Model, chat.Text(), chat.Choices[0].FinishReason)
	if chat.Text() == "" {
		t.Error("empty completion")
	}
}
