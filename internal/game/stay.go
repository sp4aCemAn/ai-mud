package game

import (
	"fmt"
	"log/slog"
)

// Slice 4: the innkeeper's stay. Bump the innkeeper → the stay opens
// with a fixed 5-coin cost (paid up front); the game tick advances the
// sleep sequence (a staged line every ~2s, ~10s total); at completion
// the inn heals HP and mana fully. ESC cancels — the coins already
// changed hands (the bed was taken).

const stayCost = 5
const stayTicksPerStage = 4 // 500ms tick cadence → a stage each ~2s

var stayLines = []string{
	"you curl up in the straw bed by the hearth",
	"the town's night sounds settle — sleep takes hold",
	"a dream of dark roads you didn't take",
	"dawn creeps through the shutters",
}

// Stay is the UI-facing sleep state (mirrors Fight/Shop/Talk). With
// Offer set it's the innkeeper's STANDING MENU — the coin hasn't
// changed hands yet; enter accepts, esc declines.
type Stay struct {
	TownName  string
	Keeper    string
	Cost      int // the door price (only on the offer)
	Offer     bool
	Stage     int  // the currently shown line index (0..len-1)
	Ticked    int  // ticks accumulated in the stage
	Healed    bool // the completion flag: full HP/mana now
	Cancelled bool
	Lines     []string
}

// Out accessors keep the tick loop clean (no mixed-case two-truths).
func (st *Stay) CancelledOut() bool { return st.Cancelled }
func (st *Stay) HealedOut() bool    { return st.Healed }

// offerStay: bumping the innkeep opens the standing menu — no coin
// moves until the player accepts.
func (s *Server) offerStay(p *Player, t *TownState, i int) {
	npc := t.Dots[i]
	if s.stayOffers == nil {
		s.stayOffers = make(map[string]*Stay)
	}
	s.stayOffers[p.Fingerprint] = &Stay{
		TownName: t.Name, Keeper: npc.Name, Cost: stayCost, Offer: true,
	}
	s.state.setEvent(p.Fingerprint, fmt.Sprintf("%s: \"a bed takes %d coins\"", npc.Name, stayCost))
}

// acceptStay: [enter] on the offer — the bed is bought and the sleep
// sequence starts. A player short on coin keeps the offer open.
func (s *Server) acceptStay(p *Player, off *Stay) {
	w := s.state
	if p.Coins < stayCost {
		delete(s.stayOffers, p.Fingerprint)
		w.setEvent(p.Fingerprint, fmt.Sprintf("%s: \"the bed takes %d coins — you're short\"", off.Keeper, stayCost))
		return
	}
	p.Coins -= stayCost
	if s.stays == nil {
		s.stays = make(map[string]*Stay)
	}
	s.stays[p.Fingerprint] = &Stay{
		TownName: off.TownName, Keeper: off.Keeper,
		Lines: append([]string{"you curl up in the straw by the hearth…"}, stayLines...),
	}
	w.setEvent(p.Fingerprint, fmt.Sprintf("%s takes %d coins — \"sleep well\"", off.Keeper, stayCost))
}

// declineStay: [esc] on the offer — nobody was committed to a bed, so
// nothing was ever spent.
func (s *Server) declineStay(p *Player) {
	off, ok := s.stayOffers[p.Fingerprint]
	if !ok {
		return
	}
	delete(s.stayOffers, p.Fingerprint)
	w := s.state
	w.setEvent(p.Fingerprint, fmt.Sprintf("you keep your coin — %s goes back to wiping the bar", off.Keeper))
}

// advanceStaysLocked: the tick's sleep hook (called with the players
// lock held from tick()).
func (s *Server) advanceStaysLocked() {
	w := s.state
	for fp, stay := range s.stays {
		if stay.HealedOut() || stay.CancelledOut() {
			delete(s.stays, fp)
			continue
		}
		p := s.players[fp]
		if p == nil { // left mid-sleep: nothing owed beyond the coins
			delete(s.stays, fp)
			continue
		}
		if w.ticks%stayTicksPerStage != 0 {
			continue
		}
		stay.Stage++
		if stay.Stage < len(stay.Lines) {
			w.setEvent(fp, stay.Lines[stay.Stage])
			continue
		}
		// the stay completes: full restore
		stay.Healed = true
		p.HP = p.MaxHP
		p.Mana = p.MaxMana
		w.setEvent(fp, "you wake to the hearth's embers — fully restored")
		delete(s.stays, fp)
		slog.Info("stay completed", "town", stay.TownName, "fp", fp)
	}
}

// stayMirror: the running sleep sequence wins; otherwise the standing
// offer. The Result channels the right state to the UI's overlay.
func (s *Server) stayMirror(fp string) *Stay {
	if stay := s.stays[fp]; stay != nil {
		return stay
	}
	return s.stayOffers[fp]
}

// cancelStay: ESC; the coins stay spent (the bed was taken), the rest
// of the night doesn't happen.
func (s *Server) cancelStay(p *Player) {
	if _, ok := s.stays[p.Fingerprint]; ok {
		delete(s.stays, p.Fingerprint)
		w := s.state
		w.setEvent(p.Fingerprint, "you climb out of bed mid-rest — mind the hangover")
		slog.Info("stay cancelled", "fp", p.Fingerprint)
	}
}

// sleepLocked: the movement/hover pin — resting players can't walk
// (isBubble the same way the fight pin holds).
func (s *Server) sleepingLocked(p *Player) bool {
	_, asleep := s.stays[p.Fingerprint]
	return asleep
}
