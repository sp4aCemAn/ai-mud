// Command mockllm serves a tiny OpenAI-compatible chat endpoint used by
// the harness-mock CI workflow: the point is to verify the server can
// boot with the harness configured while the LLM is fake — the
// game-master loop talks to this and the game keeps serving.
//
// Endpoints:
//
//	GET  /v1/models        — a stub model list (matches harness client)
//	POST /v1/chat/completions — a fixed "nothing to add" completion
package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data":   []map[string]any{{"id": "mock-gm", "object": "model"}},
		})
	})

	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":    "mock-1",
			"model": "mock-gm",
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "the world holds its breath"}},
			},
		})
	})

	addr := "127.0.0.1:1234"
	log.Printf("mock LLM listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
