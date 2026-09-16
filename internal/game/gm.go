package game

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/sp4aceman/ai-mud/internal/storage"
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
// Frontier pokes ALSO signal the harness door (the queue's trigger —
// the explore event wakes the game master without a timer poll).
func (s *Server) gmNote(kind, text string) {
	s.state.gmEvents = append(s.state.gmEvents, GMEvent{Kind: kind, Text: text})
	if len(s.state.gmEvents) > gmEventCap {
		s.state.gmEvents = s.state.gmEvents[len(s.state.gmEvents)-gmEventCap:]
	}
	if kind == "explore" {
		select {
		case s.gmWake <- struct{}{}: // coalesces: the door is a 1-buffer
		default:
		}
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

// TerrainCard is the frontier readout: which chunks are settled vs
// still dark, and the population cap the rails enforce (the model
// reads facts; the settle-degree lives here, not in prose guesswork).
type TerrainCard struct {
	Settled  int    `json:"settled_chunks"`  // chunks at/over the cap
	Frontier int    `json:"frontier_chunks"` // unexplored, touching settled
	Cap      int    `json:"settle_cap"`      // entities per chunk before "full"
	Chunk    [2]int `json:"player_chunk"`    // where the player stands now
}

// Poke is the frontier door (the harness's queue trigger). The door
// coalesces (1-buffer) — multiple explorations per cadence = one wake.
func (s *Server) Poke() <-chan struct{} { return s.gmWake }

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
	World   WorldSummary `json:"world"`
	Towns   []TownCard   `json:"towns"`
	Terrain TerrainCard  `json:"terrain"`
	Events  []GMEvent    `json:"events"`
}

// Observe snapshots the game master's readout (drains the event ring —
// the harness is the only observer). NO lock is held across the inner
// calls: WorldSummary/takePlayers take playersMu themselves
// (non-reentrant — a double-lock here would freeze the harness goroutine).
func (s *Server) Observe() GMReadout {
	out := GMReadout{World: s.WorldSummary(""), Towns: s.Towns()}
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	out.Events = s.state.gmEvents
	s.state.gmEvents = nil
	out.Terrain = s.Terrain()
	return out
}

// Terrain builds the frontier card (playersMu held).
func (s *Server) Terrain() TerrainCard {
	w := s.state
	card := TerrainCard{Cap: settleCap}
	Pcx, Pcy := chunkOf(w.spawnX, w.spawnY)
	if p, ok := s.playerFrontier(); ok {
		Pcx, Pcy = chunkOf(p.X, p.Y)
	}
	card.Chunk = [2]int{Pcx, Pcy}
	for k, n := range w.settled {
		if n >= settleCap {
			card.Settled++
		} else if frontierNeighbor(w, k) {
			card.Frontier++
		}
	}
	return card
}

// playerFrontier reads the first live player's tile for the terrain
// card (the frontier is read around the player; fine while solo).
func (s *Server) playerFrontier() (*Player, bool) {
	for _, p := range s.players {
		return p, true
	}
	return nil, false
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

// AnnounceLocation: the DEBUG rail — every content verb announces its
// own coordinates, so a player (and the operator) can FIND what the
// GM just built among the map's commas. The GM speaks in fiction;
// the rails speak the truth of the world.
func (s *Server) AnnounceLocation(what string, x, y int) {
	s.GMAnnounce(fmt.Sprintf("(at %d,%d) %s", x, y, what))
}

// GMSpawnEnemies: the frontier spawn verb — the GM says WHAT and HOW
// tough; the rails pick WHERE (a walkable tile on the settled border —
// the model never does coordinates) and cap the size.
func (s *Server) GMSpawnEnemies(count, level int) error {
	if count < 1 {
		count = 1
	}
	if count > 4 {
		count = 4
	}
	if level < 1 {
		level = 1
	}
	if level > 3 {
		level = 3
	}
	x, y, ok := s.GMFrontierSpot()
	if !ok {
		return fmt.Errorf("no frontier country to settle")
	}
	name := pickName(gmBandNames)
	_, err := s.SpawnEnemyGroup(SpawnEnemyGroupSpec{
		Name: name, Count: count, Level: level, X: &x, Y: &y,
	})
	if err != nil {
		return fmt.Errorf("spawn refused: %w", err)
	}
	s.AnnounceLocation(fmt.Sprintf("a band of %s stirs (enemies ahead)", name), x, y)
	slog.Info("gm spawned", "name", name, "count", count, "level", level,
		"at", fmt.Sprintf("%d,%d", x, y))
	return nil
}

// GMRaiseVillage: the GM names a settlement; the rails land it on the
// frontier with a DEFAULT roster (innkeep + storekeep + villager, the
// store wired to the global catalog) — so every village the GM invents
// is real: enterable, talkable, tradeable. Nothing destructive rides
// this path (raising is additive; removal stays admin-only).
func (s *Server) GMRaiseVillage(name string) error {
	name = strings.TrimSpace(strings.Join(strings.Fields(name), " "))
	if name == "" {
		return fmt.Errorf("a village needs a name")
	}
	if len([]rune(name)) > 32 {
		name = string([]rune(name)[:32])
	}
	// name collisions: suffix with folk-count flavor and keep going
	for _, t := range s.Towns() {
		if strings.EqualFold(t.Name, name) {
			name = fmt.Sprintf("%s II", name)
		}
	}
	x, y, ok := s.GMFrontierSpot()
	if !ok {
		return fmt.Errorf("no frontier country to raise a town on")
	}
	payload := defaultVillagePayload()
	obj, err := s.PlaceObject(ObjectSpec{
		Kind: storage.ObjectVillage, Name: name, Radius: 4,
		X: &x, Y: &y, Data: payload,
	})
	if err != nil {
		return fmt.Errorf("the village could not be raised: %w", err)
	}
	s.AnnounceLocation(fmt.Sprintf(
		"the gates of %s rise at the frontier — a gate, an inn, a store",
		name), x, y)
	slog.Info("gm raised a village", "name", name, "id", obj.ID,
		"at", fmt.Sprintf("%d,%d", x, y), "door_tile", s.state.tileAt(x, y))
	return nil
}
