package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/sp4aceman/ai-mud/internal/game"
)

// --- json plumbing -----------------------------------------------------------

// writeJSON answers every 2xx with a body解码 — tools read it back.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// readJSON bounds and decodes a request body into v.
func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

// failJSON turns an error into a {"error": ...} reply.
func failJSON(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var httpErr *httpStatusError
	if errors.As(err, &httpErr) {
		status = httpErr.status
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

type httpStatusError struct {
	status int
	msg    string
}

func (e *httpStatusError) Error() string { return e.msg }

func notFound(what string) error {
	return &httpStatusError{status: http.StatusNotFound, msg: fmt.Sprintf("%s not found", what)}
}

// --- routes ------------------------------------------------------------------

// ToolRoute is everything the AI game master (and any operator) can do
// to the live world from outside the SSH session. The game logic stays
// in internal/game; this layer only transports JSON.
func toolRoutes(gs *game.Server) http.Handler {
	r := http.NewServeMux()

	// GET /api/world → live summary (dims, dots, version, players)
	r.HandleFunc("GET /api/world", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, gs.WorldSummary())
	})

	// GET /api/world/enemies → the live hostile groups (readback)
	r.HandleFunc("GET /api/world/enemies", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, gs.Enemies())
	})

	// POST /api/world/enemies {name?, count, level, x? y?} → spawn
	r.HandleFunc("POST /api/world/enemies", func(w http.ResponseWriter, r *http.Request) {
		var spec game.SpawnEnemyGroupSpec
		if err := readJSON(w, r, &spec); err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "bad spawn payload: " + err.Error()})
			return
		}
		e, err := gs.SpawnEnemyGroup(spec)
		if err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, e)
	})

	// DELETE /api/world/enemies/42 → despawn group 42
	r.HandleFunc("DELETE /api/world/enemies/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := pathID(r.PathValue("id"))
		if err != nil {
			failJSON(w, err)
			return
		}
		if !gs.DespawnEnemy(id) {
			failJSON(w, notFound("enemy group"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// POST /api/world/announce {text} → global world-event line
	r.HandleFunc("POST /api/world/announce", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		if err := readJSON(w, r, &body); err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "bad announce payload: " + err.Error()})
			return
		}
		if body.Text == "" {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "text required"})
			return
		}
		gs.Announce(body.Text)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "announced"})
	})

	return r
}

func pathID(s string) (int, error) {
	var id int
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, &httpStatusError{http.StatusBadRequest, "bad id"}
	}
	return id, nil
}
