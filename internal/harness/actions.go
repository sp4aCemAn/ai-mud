package harness

import (
	"encoding/json"
	"strings"
)

// GMAction is one parsed action from either stage (the loop's idle
// union type: the stage-2 native tool calls flatten into this too).
// Flat fields only — the simple-tool-call contract for a 2B model.
type GMAction struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`  // announce
	Name  string `json:"name,omitempty"`  // raise_village
	Count int    `json:"count,omitempty"` // spawn_enemies
	Level int    `json:"level,omitempty"` // spawn_enemies
}

// parseActions is the FALLBACK text-protocol parser (single-stage
// degration mode: a backend without native tool calling still speaks
// the {"actions":[...]} shape). Tolerant of the local models' habits:
// fenced JSON, prose around the object, a bare action object. Pure
// prose is a NO-OP (never noise-flooded players).
func parseActions(text string) []GMAction {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	// strip the code fences the models love to wrap JSON in
	if i := strings.Index(text, "{"); i >= 0 {
		end := strings.LastIndex(text, "}")
		if end > i {
			text = text[i : end+1]
		}
	}
	// a single action object without the "actions" wrapper (decode
	// order matters: a bare action must not read as an empty hold)
	var single GMAction
	if err := json.Unmarshal([]byte(text), &single); err == nil && single.Type != "" {
		return []GMAction{single}
	}
	var wrapper struct {
		Actions []GMAction `json:"actions"`
	}
	if err := json.Unmarshal([]byte(text), &wrapper); err == nil {
		if len(wrapper.Actions) == 0 {
			return nil // the model explicitly chose to hold — respect it
		}
		return wrapper.Actions
	}
	// pure prose — the models' chit-chat stays a NO-OP: announcing it
	// would flood player event lines with reasoning noise (the mock's
	// "the world holds its breath" is exactly this shape).
	return nil
}
