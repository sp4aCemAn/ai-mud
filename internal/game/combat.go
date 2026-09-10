package game

import (
	"fmt"
	"math/rand"
	"time"
)

// Enemy is one hostile dot — a group of baddies sharing a single tile
// in the zoomed-out view. HP is the whole group's.
type Enemy struct {
	ID        int
	Name      string
	X, Y      int
	Count     int
	HP, MaxHP int
	Level     int
	respawnAt time.Time // zero while alive
}

// fightNames are the dark themed group baddies.
var fightNames = []string{
	"carrion ghouls", "black choir", "rust hounds", "skeletal wardens",
	"grinning phantasms", "the unshriven",
}

// Fight is the UI-facing combat state. Turn-based: the player acts,
// then the enemy strikes (unless it died or the player slipped away).
type Fight struct {
	EnemyID       int
	Name          string
	Count         int
	Round         int
	PlayerHP      int
	PlayerMaxHP   int
	EnemyHP       int
	EnemyMaxHP    int
	Last, LastFor string // one-line log of what the enemy just did
}

// worldState is everything the Server owns beyond players. Guarded by
// the same mutex as the player map.
type worldState struct {
	ww, wh      int // world dims (dynamic — Resize regenerates)
	tiles       []string
	spawnX      int
	spawnY      int
	region      [][2]int // the main land region — where dots may live
	npcDot      Dot
	enemies     map[int]*Enemy
	nextEnemyID int
	fights      map[string]*Fight   // fingerprint → open fight
	shops       map[string]bool     // fingerprint → store open
	lastEvents  map[string][]string // fingerprint → latest log lines
	rnd         *rand.Rand
	maxGroups   int
	ticks       int
}

// setEvent stores the latest line or two per player — the UI shows
// these as a small log under the field.
func (w *worldState) setEvent(fp, line string) {
	if w.lastEvents == nil {
		w.lastEvents = make(map[string][]string)
	}
	log := w.lastEvents[fp]
	log = append(log, line)
	if len(log) > 3 {
		log = log[len(log)-3:]
	}
	w.lastEvents[fp] = log
}

// enemyGroupHP scales with the group size so a dot of five bites.
func enemyHP(r *rand.Rand, count, level int) (int, int) {
	max := 5*count + level*2
	return r.Intn(max/2+1) + max/2, max
}

// spawnWorld generates terrain (at ww×wh) and populates it. Called at
// server construction and on Resize.
func spawnWorld(s *Server, r *rand.Rand, ww, wh int) {
	tiles, sx, sy := genTerrain(r, ww, wh)
	w := &worldState{
		ww:        ww,
		wh:        wh,
		tiles:     tiles,
		spawnX:    sx,
		spawnY:    sy,
		region:    mainRegion(tiles),
		enemies:   make(map[int]*Enemy),
		fights:    make(map[string]*Fight),
		shops:     make(map[string]bool),
		rnd:       r,
		maxGroups: 4,
	}
	w.npcDot = newMerchant(w, sx, sy)
	for i := 0; i < w.maxGroups; i++ {
		w.spawnEnemy()
	}

	s.state = w
}

// spawnEnemy creates one live group on a free walkable tile.
func (w *worldState) spawnEnemy() {
	x, y := w.randomSpot()
	count := w.rnd.Intn(4) + 1 // 1–4 baddies per dot
	lvl := w.rnd.Intn(3) + 1
	hp, max := enemyHP(w.rnd, count, lvl)
	id := w.nextEnemyID
	w.nextEnemyID++
	w.enemies[id] = &Enemy{
		ID:   id,
		Name: fightNames[w.rnd.Intn(len(fightNames))],
		X:    x, Y: y,
		Count: count,
		HP:    hp, MaxHP: max,
		Level: lvl,
	}
}

func (w *worldState) randomSpot() (int, int) {
	return w.pickRegionSpot(w.spawnX, w.spawnY, 4)
}

// pickRegionSpot picks an unoccupied main-region tile at least dist
// steps from (sx, sy) — fallback relaxes distance until any free cell.
func (w *worldState) pickRegionSpot(sx, sy, dist int) (int, int) {
	for d := dist; d >= 0; d-- {
		for tries := 0; tries < 200; tries++ {
			cell := w.region[w.rnd.Intn(len(w.region))]
			if abs(cell[0]-sx)+abs(cell[1]-sy) >= d && !w.occupiedBy(cell[0], cell[1]) {
				return cell[0], cell[1]
			}
		}
	}
	// any free region cell
	for _, cell := range w.region {
		if !w.occupiedBy(cell[0], cell[1]) {
			return cell[0], cell[1]
		}
	}
	return sx, sy
}

// occupiedBy reports a live enemy or the merchant on the tile.
func (w *worldState) occupiedBy(x, y int) bool {
	if w.npcDot.X == x && w.npcDot.Y == y {
		return true
	}
	return w.enemyAt(x, y) != nil
}

// occupied reports any dot on the tile.
func (w *worldState) occupied(x, y int) (occupant string, id int) {
	if w.npcDot.X == x && w.npcDot.Y == y {
		return "npc", 0
	}
	for id, e := range w.enemies {
		if e.HP > 0 && e.X == x && e.Y == y {
			return "enemy", id
		}
	}
	return "", 0
}

// enemyAt finds the live enemy at coordinates.
func (w *worldState) enemyAt(x, y int) *Enemy {
	for _, e := range w.enemies {
		if e.HP > 0 && e.X == x && e.Y == y {
			return e
		}
	}
	return nil
}

// CombatView is the richer optional interface: movement into things
// becomes combat and stores, and turn-based actions resolve on demand.
// *Server implements both this and PlayerView.
type CombatView interface {
	// WorldView snapshots terrain + dots for rendering.
	WorldView(fp string) World
	// Interact is PlayerView.Move with world consequences. Bump into
	// water (blocked), the merchant (opens the store), or an enemy
	// dot (opens combat).
	Interact(fp string, dx, dy int) Result
	// Command dispatches everything but movement:
	//   "attack" | "cast" | "flee" — fight actions
	//   "buy", arg = shop line index
	//   "use", arg = pack item (1 potion, 2 draught)
	//   "close"  — leave the store
	Command(fp string, cmd string, arg int) Result

	// Resize regenerates the world at the client's terminal size
	// (WindowSizeMsg only — the world remakes, fights end).
	Resize(fp string, w, h int) Result
}

// Result carries everything a UI action might change at once.
type Result struct {
	Player Player
	World  World
	Fight  *Fight // current combat, nil when out of it
	Shop   *Shop  // open store, nil when none
	Events []string
}

// --- turn-based combat -------------------------------------------------------

// FightCmd implements Command's fight actions and keeps the fighter's
// side of each duel honest: the player acts, the baddies respond.
func (s *Server) runFight(fp, action string, w *worldState, p *Player) ([]string, *Fight) {
	f, open := w.fights[fp]
	if !open {
		return []string{"nothing hostile here — you strike the dark air"}, nil
	}

	enemy := w.enemies[f.EnemyID]
	events := []string{}

	// the player's half of the round
	switch action {
	case "attack":
		dmg := w.rnd.Intn(3) + 1 + p.Atk
		enemy.HP = max(0, enemy.HP-dmg)
		formatEvent(&events, fmt.Sprintf("you carve into the %s (-%d)", f.Name, dmg))
		f.PlayerHP, f.PlayerMaxHP = p.HP, p.MaxHP
	case "cast":
		if p.Mana < 3 {
			formatEvent(&events, "mana is spent — you cannot cast")
			f.Last, f.LastFor = "you fumble a spell, no mana", f.Name
			return events, fightCopy(f)
		}
		p.Mana -= 3
		dmg := w.rnd.Intn(6) + 4 + p.Atk/2
		enemy.HP = max(0, enemy.HP-dmg)
		formatEvent(&events, fmt.Sprintf("a bolt of fire sears the %s (-%d)", f.Name, dmg))
	case "flee":
		if w.rnd.Intn(5) < 3 {
			delete(w.fights, fp)
			formatEvent(&events, fmt.Sprintf("you slip from the %s's reach", f.Name))
			return events, nil
		}
		formatEvent(&events, "you turn — the pack closes on you")
	default:
		return events, fightCopy(f)
	}

	f.Round++

	if enemy.HP <= 0 {
		// killed: rewards + respawn timer + close fight
		coins := w.rnd.Intn(8) + 3 + 3*enemy.Count
		xp := 5 + 6*enemy.Count
		p.Coins += coins
		p.XP += xp
		if pot := w.rnd.Intn(5); pot < 2 {
			p.Inventory["potion"]++
			formatEvent(&events, "you pocket a dark potion from the remains")
		}
		res := []string{
			fmt.Sprintf("the %s fall silent (+%dc +%dx)", f.Name, coins, xp),
		}
		res = append(res, events...)
		p.checkLevel(w.rnd, &events)
		enemy.respawnAt = time.Now().Add(60 * time.Second)
		enemy.HP = 0
		delete(w.fights, fp)
		return res, nil
	}

	// the enemy's half — a beating if you lingered
	edmg := w.rnd.Intn(3) + 1 + enemy.Level - p.Def
	if edmg < 1 {
		edmg = 1
	}
	p.HP = max(0, p.HP-edmg)
	f.Last = fmt.Sprintf("the %s strike (%d you)", f.Name, edmg)
	f.LastFor = f.Name
	if p.HP <= 0 {
		events = append(events, fmt.Sprintf("the %s put you in the cold dirt…", f.Name))
		s.respawn(w, p)
		events = append(events, "…but the dark wakes you by the road, half your coin gone")
		delete(w.fights, fp)
		return events, nil
	}
	formatEvent(&events, f.Last)
	f.PlayerHP, f.PlayerMaxHP = p.HP, p.MaxHP
	f.EnemyHP = enemy.HP
	return events, fightCopy(f)
}

// formatEvent renders fmt strings after events is filled. Wrapper
// kept tiny on purpose.
func formatEvent(events *[]string, line string) {
	*events = append(*events, line)
}

// respawn drops a dead player back at spawn, poorer.
func (s *Server) respawn(w *worldState, p *Player) {
	p.X = w.spawnX
	p.Y = w.spawnY
	p.HP = p.MaxHP
	p.Mana = p.MaxMana
	half := p.Coins / 2
	p.Coins -= half
}

// checkLevel rolls XP thresholds and hits the player up.
func (p *Player) checkLevel(r *rand.Rand, events *[]string) {
	for p.XP >= p.Level*25 {
		p.Level++
		p.MaxHP += 4
		p.MaxMana += 2
		p.HP = p.MaxHP
		p.Mana = p.MaxMana
		formatEvent(events, fmt.Sprintf("the dark hardens you — level %d", p.Level))
	}
}

// --- view + wire into PlayerView ----

// worldSnapshot builds the render snapshot; assumes the lock is held.
func (s *Server) worldSnapshot() World {
	w := s.state
	out := World{
		W:      w.ww,
		H:      w.wh,
		Tiles:  make([]string, len(w.tiles)),
		SpawnX: w.spawnX,
		SpawnY: w.spawnY,
	}
	copy(out.Tiles, w.tiles)
	for _, e := range w.enemies {
		if e.HP > 0 {
			out.Dots = append(out.Dots, Dot{X: e.X, Y: e.Y, Kind: "enemy", Count: e.Count, Name: e.Name})
		}
	}
	out.Dots = append(out.Dots, w.npcDot)
	return out
}

// WorldView implements CombatView.
func (s *Server) WorldView(fp string) World {
	s.playersMu.Lock()
	defer s.playersMu.Unlock()
	return s.worldSnapshot()
}

func fightCopy(f *Fight) *Fight {
	c := *f
	return &c
}
