package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

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

	// GET /api/world?fp=<fingerprint> → live summary (dims, dots,
	// version, players; fp scopes the inTown nesting view)
	r.HandleFunc("GET /api/world", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, gs.WorldSummary(r.URL.Query().Get("fp")))
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

	// GET /api/world/objects?kind=… → authored placements (readback)
	r.HandleFunc("GET /api/world/objects", func(w http.ResponseWriter, r *http.Request) {
		if kind := r.URL.Query().Get("kind"); kind != "" {
			writeJSON(w, http.StatusOK, gs.Objects(kind))
			return
		}
		writeJSON(w, http.StatusOK, gs.Objects())
	})

	// POST /api/world/objects {kind, name, x?, y?, tiles?, data?} → place
	r.HandleFunc("POST /api/world/objects", func(w http.ResponseWriter, r *http.Request) {
		var spec game.ObjectSpec
		if err := readJSON(w, r, &spec); err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "bad place payload: " + err.Error()})
			return
		}
		obj, err := gs.PlaceObject(spec)
		if err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, obj)
	})

	// PATCH /api/world/objects/42 → update_object (replays from the row)
	r.HandleFunc("PATCH /api/world/objects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id64, err := pathID64(r.PathValue("id"))
		if err != nil {
			failJSON(w, err)
			return
		}
		var patch game.ObjectPatch
		if err := readJSON(w, r, &patch); err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "bad patch payload: " + err.Error()})
			return
		}
		obj, err := gs.UpdateObject(id64, patch)
		if err != nil {
			failJSON(w, notFound("authored object"))
			return
		}
		writeJSON(w, http.StatusOK, obj)
	})

	// DELETE /api/world/objects/42 → remove_object (row + live form)
	r.HandleFunc("DELETE /api/world/objects/{id}", func(w http.ResponseWriter, r *http.Request) {
		id64, err := pathID64(r.PathValue("id"))
		if err != nil {
			failJSON(w, err)
			return
		}
		if err := gs.RemoveObject(id64); err != nil {
			failJSON(w, notFound("authored object"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// POST /api/world/terrain {name?, tiles: [{x,y,g}...]} → edit_terrain
	r.HandleFunc("POST /api/world/terrain", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name  string          `json:"name"`
			Tiles json.RawMessage `json:"tiles"`
		}
		if err := readJSON(w, r, &body); err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "bad edit payload: " + err.Error()})
			return
		}
		var deltas []game.TileEdit
		if err := json.Unmarshal(body.Tiles, &deltas); err != nil || len(deltas) == 0 {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "tiles must be a non-empty [{x,y,g}] list"})
			return
		}
		obj, err := gs.TerrainEdit(game.TerrainEditSpec{Name: body.Name, Tiles: deltas})
		if err != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, err.Error()})
			return
		}
		writeJSON(w, http.StatusCreated, obj)
	})

	// GET /api/world/tile?x=&y= → one absolute tile's data glyph
	// (operator/verification tool for the infinite plane)
	r.HandleFunc("GET /api/world/tile", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		x, err1 := strconv.Atoi(q.Get("x"))
		y, err2 := strconv.Atoi(q.Get("y"))
		if err1 != nil || err2 != nil {
			failJSON(w, &httpStatusError{http.StatusBadRequest, "x and y must be ints"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"tile": string(rune(gs.TileAt(x, y)))})
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

func pathID64(s string) (int64, error) {
	var id int64
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, &httpStatusError{http.StatusBadRequest, "bad id"}
	}
	return id, nil
}
