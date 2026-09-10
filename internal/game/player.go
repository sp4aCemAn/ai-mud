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

	lastSeen time.Time
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
	slog.Info("player joined", "fingerprint", fingerprint, "name", name)
	return copyPlayer(p)
}

// Leave removes a player outright (logout / explicit disconnect).
func (s *Server) Leave(fingerprint string) {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	delete(s.players, fingerprint)
	delete(s.state.fights, fingerprint)
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
	return copyPlayer(s.interact(p, dx, dy)), true
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
	r := Result{World: s.worldSnapshot(), Player: copyPlayer(s.interact(p, dx, dy))}
	if f, open := s.state.fights[fp]; open {
		r.Fight = fightCopy(f)
	}
	if locked, _ := s.openShopLocked(p); locked {
		r.Shop = shopOpen(p)
	}
	r.Events = s.state.lastEvents[fp]
	return r
}

// lockedInteract mutates p for the bump attempt; assumes lock held.
func (s *Server) interact(p *Player, dx, dy int) *Player {
	w := s.state

	// a live fight pins the player to the duel — only flee/kill frees
	if f, open := w.fights[p.Fingerprint]; open && f != nil {
		w.setEvent(p.Fingerprint, "the fight holds you — [f]lee to escape")
		return p
	}

	nx, ny := clamp(p.X+dx, 0, w.ww-1), clamp(p.Y+dy, 0, w.wh-1)
	if !walkable(w.tiles, nx, ny) {
		w.setEvent(p.Fingerprint, "the water is dark and deep — no crossing")
		return p
	}
	if kind, id := w.occupied(nx, ny); kind != "" {
		if kind == "npc" {
			s.openShop(p)
			w.setEvent(p.Fingerprint, fmt.Sprintf("%s hails you — [1..%d] to trade", merchantName, len(wares)))
			return p
		}
		s.openFight(p, id)
		w.setEvent(p.Fingerprint, fmt.Sprintf("bump — the %s turn on you! [a]ttack [c]ast [f]lee", w.enemies[id].Name))
		return p
	}

	p.X, p.Y = nx, ny
	p.lastSeen = time.Now()
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

// openShopLocked marks the shop session open for this player.
func (s *Server) openShop(p *Player) {
	w := s.state
	if w.shops == nil {
		w.shops = map[string]bool{}
	}
	w.shops[p.Fingerprint] = true
}

func (s *Server) openShopLocked(p *Player) (open bool, _ error) {
	if s.state.shops != nil {
		open = s.state.shops[p.Fingerprint]
	}
	return open, nil
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
	case "noop", "attack", "cast", "flee":
		if cmd == "noop" {
			r := Result{Player: copyPlayer(p), World: s.worldSnapshot()}
			if f, open := w.fights[fp]; open {
				r.Fight = fightCopy(f)
			}
			if open, _ := s.openShopLocked(p); open {
				r.Shop = shopOpen(p)
			}
			r.Events = w.lastEvents[fp]
			return r
		}
		events, _ = s.runFight(fp, cmd, w, p)
	case "buy":
		events = buy(w, p, arg)
	case "use":
		events = useItem(p, arg)
	case "close":
		delete(w.shops, fp)
		events = []string{fmt.Sprintf("%s nods at the dark road behind you", merchantName)}
	}

	r := Result{
		Player: copyPlayer(p),
		World:  s.worldSnapshot(),
	}
	if f, open := w.fights[fp]; open {
		r.Fight = fightCopy(f)
	}
	if open, _ := s.openShopLocked(p); open {
		r.Shop = shopOpen(p)
	}
	r.Events = events
	return r
}

// useItem applies pack items: arg 1 = dark potion, arg 2 = draught.
func useItem(p *Player, arg int) []string {
	switch arg {
	case 1:
		if p.Inventory["dark potions"] < 1 {
			return []string{"no dark potions in the pack"}
		}
		p.Inventory["dark potions"]--
		p.HP = min(p.HP+8, p.MaxHP)
		return []string{"the potion is bitter — wounds close (hp restored)"}
	case 2:
		if p.Inventory["mana draughts"] < 1 {
			return []string{"no mana draughts in the pack"}
		}
		p.Inventory["mana draughts"]--
		p.Mana = min(p.Mana+4, p.MaxMana)
		return []string{"the draught blurs the air — mana flows back"}
	}
	return []string{"nothing like that in the pack"}
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
			delete(s.state.shops, fp)
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
