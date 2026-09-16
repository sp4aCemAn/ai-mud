package game

import (
	"fmt"
	"log/slog"
	"time"
)

// The playable world plus the player state and everything a body on
// the map can do. The player is one dot among terrain, enemy dots and
// the merchant dot; combat and shopping are turn-based, no timers.

const (
	// staleAfter is how long a player survives without being heard from.
	// The tick loop reaps older players — the stand-in for disconnect
	// handling until sessions signal their own teardown.
	staleAfter = 30 * time.Second

	// starting purse and basics
	startCoins = 10
	startAtk   = 1
	startDef   = 0
)

// Player is the client-side-visible state of one adventurer.
// Identifying key: the SSH key fingerprint ("" = anonymous/guest).
type Player struct {
	Fingerprint string
	Name        string
	Level       int
	HP          int
	MaxHP       int
	Mana        int
	MaxMana     int
	Coins       int
	XP          int
	Atk, Def    int
	X, Y        int
	Inventory   map[string]int

	// townRef is the nested-room pointer: nil = walking the world;
	// non-nil = inside a TownState (The X/Y are town-space then, and
	// ReturnX/ReturnY hold the world tile the gate was stepped from).
	townRef *townRef

	// quests: bounties granted by talk closings (slice 3)
	quests []Quest

	lastSeen time.Time
}

// townRef is one player's nesting position (which town + their door).
type townRef struct {
	TownID           int64
	ReturnX, ReturnY int
}

// PlayerView is the narrow interface the UI uses to play. Implemented
// by *Server; swappable for a networked client or a fake in tests.
type PlayerView interface {
	// Join registers (or returns) the player for this fingerprint.
	Join(fingerprint, name string) Player
	// State returns a copy of the player's current state.
	State(fingerprint string) (Player, bool)
	// Move applies a delta and returns the updated state. Returns
	// false if the fingerprint is unknown.
	Move(fingerprint string, dx, dy int) (Player, bool)
}

// copyPlayer deep-copies the mutable bits so callers never hold
// references to live server state.
func copyPlayer(p *Player) Player {
	c := *p
	if p.Inventory != nil {
		c.Inventory = make(map[string]int, len(p.Inventory))
		for k, v := range p.Inventory {
			c.Inventory[k] = v
		}
	}
	return c
}

// Join registers (or returns the existing) player for a fingerprint.
// A new player spawns on the main land region with starting stats.
func (s *Server) Join(fingerprint, name string) Player {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	if p, ok := s.players[fingerprint]; ok {
		p.lastSeen = time.Now()
		return copyPlayer(p)
	}

	w := s.state
	p := &Player{
		Fingerprint: fingerprint,
		Name:        name,
		Level:       1,
		HP:          12,
		MaxHP:       12,
		Mana:        6,
		MaxMana:     6,
		Coins:       startCoins,
		XP:          0,
		Atk:         startAtk,
		Def:         startDef,
		X:           w.spawnX,
		Y:           w.spawnY,
		Inventory:   map[string]int{},
		lastSeen:    time.Now(),
	}
	s.players[fingerprint] = p
	s.gmNote("login", fmt.Sprintf("%s enters the world", name))
	slog.Info("player joined", "fingerprint", fingerprint, "name", name)
	return copyPlayer(p)
}

// Leave removes a player outright (logout / explicit disconnect).
func (s *Server) Leave(fingerprint string) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	delete(s.players, fingerprint)
	delete(s.state.fights, fingerprint)
	delete(s.talks, fingerprint)
	delete(s.stays, fingerprint)
	delete(s.stayOffers, fingerprint)
}

// State implements PlayerView.
func (s *Server) State(fingerprint string) (Player, bool) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	p, ok := s.players[fingerprint]
	if !ok {
		return Player{}, false
	}
	p.lastSeen = time.Now()
	return copyPlayer(p), true
}

// Move implements PlayerView on top of Interact.
func (s *Server) Move(fingerprint string, dx, dy int) (Player, bool) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	p, ok := s.players[fingerprint]
	if !ok {
		return Player{}, false
	}
	return copyPlayer(s.lockedInteract(p, dx, dy)), true
}

// Interact implements CombatView: bumping into water blocks, the
// merchant opens the store, an enemy dot opens a fight.
func (s *Server) Interact(fp string, dx, dy int) Result {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	p, ok := s.players[fp]
	if !ok {
		return Result{}
	}
	// order matters: the interact runs FIRST, then the view reads the
	// nesting state (entering/exiting a town swaps the world in the
	// same keystroke — the view must not lag a frame behind)
	np := copyPlayer(s.lockedInteract(p, dx, dy))
	r := Result{World: s.currentWorld(fp), Player: np}
	// slice 3: the open talk session (the UI's overlay) + the quest book
	if talk := s.talks[fp]; talk != nil {
		r.Talk = talk
	}
	if stay := s.stayMirror(fp); stay != nil {
		r.Stay = stay
	}
	r.Quests = append(r.Quests, np.quests...)
	if f, open := s.state.fights[fp]; open {
		r.Fight = fightCopy(f)
	}
	if sh := s.activeShopFor(p); sh != nil {
		r.Shop = sh
	}
	r.Events = s.state.lastEvents[fp]
	return r
}

// interact mutates p for the bump attempt; assumes lock held.
func (s *Server) lockedInteract(p *Player, dx, dy int) *Player {
	w := s.state

	// the sleep pin: resting players can't walk (esc climbs out)
	if s.sleepingLocked(p) {
		w.setEvent(p.Fingerprint, "you're mid-sleep — [esc] climbs out")
		return p
	}

	// a live fight pins the player to the duel — only flee/kill frees
	if f, open := w.fights[p.Fingerprint]; open && f != nil {
		w.setEvent(p.Fingerprint, "the fight holds you — [f]lee to escape")
		return p
	}

	// nested-room dispatch: inside a town, walk the town's grid
	if p.townRef != nil {
		return s.inTownInteract(p, dx, dy)
	}

	// the world is infinite: roaming past the served rect grows it a
	// chunk at a time (the movement's requested tile must be covered)
	s.absorbInto(p.X+dx, p.Y+dy, dx, dy)
	nx, ny := p.X+dx, p.Y+dy
	if !w.walkableAt(nx, ny) {
		w.setEvent(p.Fingerprint, "the water is dark and deep — no crossing")
		return p
	}
	// a 町 tile claimed by a village row is a DOOR — step onto it to
	// enter; an unclaimed 町 stays paint (slice 1's walkable semantics)
	if w.tileAt(nx, ny) == tileGate {
		if tid := s.townAt(nx, ny); tid != 0 {
			s.enterTown(p, tid)
			return p
		}
		// no town claims it: keep walking (paint)
	}
	if kind, id := w.occupied(nx, ny); kind != "" {
		if kind == "npc" {
			s.openStore(p, wanderStoreKey, wanderStore())
			w.setEvent(p.Fingerprint, fmt.Sprintf("%s hails you — [1..%d] to trade",
				merchantName, len(wanderStore().Lines)))
			return p
		}
		s.openFight(p, id)
		w.setEvent(p.Fingerprint, fmt.Sprintf("bump — the %s turn on you! [a]ttack [c]ast [f]lee", w.enemies[id].Name))
		return p
	}

	p.X, p.Y = nx, ny
	p.lastSeen = time.Now()
	// a step leaves the innkeeper's standing menu and any counter
	// browse behind
	delete(s.stayOffers, p.Fingerprint)
	delete(s.storeOf, p.Fingerprint)
	return p
}

// openFight opens (or refreshes) the fight with one enemy dot.
func (s *Server) openFight(p *Player, enemyID int) {
	w := s.state
	e := w.enemies[enemyID]
	if f, open := w.fights[p.Fingerprint]; open && f.EnemyID == enemyID {
		return // still circling the same group
	}
	f := &Fight{
		EnemyID:     enemyID,
		Name:        e.Name,
		Count:       e.Count,
		PlayerHP:    p.HP,
		PlayerMaxHP: p.MaxHP,
		EnemyHP:     e.HP,
		EnemyMaxHP:  e.MaxHP,
	}
	w.fights[p.Fingerprint] = f
}

// openStore mounts a store session: the Store object is the SHARED
// counter (keyed by the store, not the player — public areas), the
// player holds one browsing ref at a time. Bumping an already-open
// counter re-browses it; another counter swaps your ref.
func (s *Server) openStore(p *Player, key string, sh *Store) {
	if s.stores == nil {
		s.stores = make(map[string]*Store)
	}
	if s.storeOf == nil {
		s.storeOf = make(map[string]string)
	}
	if s.stores[key] == nil {
		s.stores[key] = sh
	}
	s.storeOf[p.Fingerprint] = key
}

// activeShopFor materializes the shop mirror for one player: the
// shared counter they're browsing, dress their purse into the pitch.
func (s *Server) activeShopFor(p *Player) *Shop {
	key, ok := s.storeOf[p.Fingerprint]
	if !ok {
		return nil
	}
	sh := s.stores[key]
	if sh == nil {
		return nil
	}
	return sh.pitchFor(p)
}

// Resize implements CombatView: rebuilds the world at the terminal's
// size and returns the caller's fresh position.
func (s *Server) Resize(fp string, w, h int) Result {
	s.regenWorld(w, h)
	return s.Command(fp, "noop", 0) // cheap fresh snapshot of the new world
}

// Command implements CombatView's action dispatch.
func (s *Server) Command(fp string, cmd string, arg int) Result {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()

	p, ok := s.players[fp]
	if !ok {
		return Result{}
	}
	w := s.state
	var events []string

	switch cmd {
	case "talk-advance": // slice 3: enter advances the conversation
		s.advanceTalk(p)
	case "talk-close": // esc = walk away from the conversation
		delete(s.talks, fp)
		w.setEvent(fp, "you walk away mid-sentence")
	case "stay-cancel": // esc during the sleep sequence
		s.cancelStay(p)
	case "stay-accept": // enter on the innkeep's standing menu
		if off := s.stayOffers[fp]; off != nil {
			delete(s.stayOffers, fp)
			s.acceptStay(p, off)
		}
	case "stay-decline": // esc on the offer — nothing was ever spent
		s.declineStay(p)
	case "noop", "attack", "cast", "flee":
		if cmd == "noop" {
			r := Result{Player: copyPlayer(p), World: s.currentWorld(fp)}
			if f, open := w.fights[fp]; open {
				r.Fight = fightCopy(f)
			}
			if sh := s.activeShopFor(p); sh != nil {
				r.Shop = sh
			}
			if talk := s.talks[fp]; talk != nil {
				r.Talk = talk
			}
			if stay := s.stayMirror(fp); stay != nil {
				r.Stay = stay
			}
			r.Events = w.lastEvents[fp]
			return r
		}
		events, _ = s.runFight(fp, cmd, w, p)
	case "buy":
		events = s.buyFromRef(w, p, arg)
	case "use":
		events = useItem(p, arg)
	case "close":
		if key, ok := s.storeOf[fp]; ok {
			if sh := s.stores[key]; sh != nil {
				w.setEvent(fp, fmt.Sprintf("%s nods at the dark road behind you", sh.Keeper))
			}
		}
		delete(s.storeOf, fp)
	}

	r := Result{
		Player: copyPlayer(p),
		World:  s.currentWorld(fp),
	}
	if f, open := w.fights[fp]; open {
		r.Fight = fightCopy(f)
	}
	if sh := s.activeShopFor(p); sh != nil {
		r.Shop = sh
	}
	if talk := s.talks[fp]; talk != nil {
		r.Talk = talk
	}
	if stay := s.stayMirror(fp); stay != nil {
		r.Stay = stay
	}
	r.Quests = append(r.Quests, r.Player.quests...)
	r.Events = events
	return r
}

// buyFromRef buys from the counter the player is browsing; a stray
// buy with no mounted store browses the wander cart (the world's
// always-open peddler contract stays).
func (s *Server) buyFromRef(w *worldState, p *Player, arg int) []string {
	key, ok := s.storeOf[p.Fingerprint]
	if !ok {
		return wanderStore().buyFrom(w, p, arg)
	}
	sh := s.stores[key]
	if sh == nil {
		return []string{"the counter folded away while you browsed."}
	}
	return sh.buyFrom(w, p, arg)
}

// reapStale drops players that haven't been heard from recently. Called
// from the tick loop; the stand-in for disconnect detection.
func (s *Server) reapStale(now time.Time) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	for fp, p := range s.players {
		if now.Sub(p.lastSeen) > staleAfter {
			slog.Info("reaping stale player", "fingerprint", fp, "name", p.Name)
			delete(s.players, fp)
			delete(s.state.fights, fp)
			delete(s.storeOf, fp)
		}
	}
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
