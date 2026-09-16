package game

import (
	"fmt"
	"strings"
)

// The game-master seam (harness slice 1). The AI observer reads a
// COMPACT readout of the world and a drained event ring; the verbs it
// may call already exist and carry their own rails (validation, caps,
// the door-claim reject). The harness never touches server internals.

// GMEvent is one world event the game master's roundup may observe.
type GMEvent struct {
	Kind string `json:"kind"` // login | death | kill | quest | loot | spawn | world | tool
	Text string `json:"text"`
}

const gmEventCap = 64 // the ring — the oldest lines fall off

// gmNote records one event into the roundup ring (playersMu held).
func (s *Server) gmNote(kind, text string) {
	s.state.gmEvents = append(s.state.gmEvents, GMEvent{Kind: kind, Text: text})
	if len(s.state.gmEvents) > gmEventCap {
		s.state.gmEvents = s.state.gmEvents[len(s.state.gmEvents)-gmEventCap:]
	}
}

// DrainGMEvents hands the roundup to the game master and clears the
// ring (the observer is the only reader).
func (s *Server) DrainGMEvents() []GMEvent {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	out := s.state.gmEvents
	s.state.gmEvents = nil
	return out
}

// TownCard is one town's readout line for the game master's prompt.
type TownCard struct {
	ID    int64    `json:"id"`
	Name  string   `json:"name"`
	Door  [2]int   `json:"door"`
	Dots  int      `json:"npcs"`
	Roles []string `json:"roles"`
}

// Towns snapshots the village registry (the world's town cards).
func (s *Server) Towns() []TownCard {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	out := make([]TownCard, 0, len(s.townRows))
	for id, row := range s.townRows {
		t := s.towns[id]
		card := TownCard{ID: id, Name: row.Name,
			Door:  [2]int{row.HomeX, row.HomeY},
			Roles: []string{}}
		if t != nil {
			card.Dots = len(t.Dots)
			for _, d := range t.Dots {
				if d.Role != "" {
					card.Roles = append(card.Roles, d.Role)
				}
			}
		}
		out = append(out, card)
	}
	return out
}

// TrackSpawn finds a town's live layout for the GM (nil when unbuilt).
func (s *Server) TrackSpawn(townID int64) *TownState {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	return s.towns[townID]
}

// GMReadout is the observational readout the harness takes each cycle:
// the world card, the town cards, the drained events. Small on purpose —
// the local model gets short, structured context.
type GMReadout struct {
	World  WorldSummary `json:"world"`
	Towns  []TownCard   `json:"towns"`
	Events []GMEvent    `json:"events"`
}

// Observe snapshots the game master's readout (drains the event ring —
// the harness is the only observer). NO lock is held across the inner
// calls: WorldSummary/Towns take playersMu themselves (non-reentrant —
// a double-lock here would freeze the whole harness goroutine).
func (s *Server) Observe() GMReadout {
	return GMReadout{
		World:  s.WorldSummary(""),
		Towns:  s.Towns(),
		Events: s.DrainGMEvents(),
	}
}

// gmAnnounce: the one verb the harness slice 1 contract carries — the
// length cap lives here so a rambling model can't flood event lines.
func (s *Server) GMAnnounce(text string) error {
	text = strings.TrimSpace(strings.Join(strings.Fields(text), " "))
	if text == "" {
		return fmt.Errorf("empty announcement")
	}
	if len(text) > 200 {
		text = text[:200]
	}
	s.Announce(text)
	return nil
}
