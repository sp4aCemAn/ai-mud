package game

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Slice 3: NPC conversations. Bumping a town NPC opens a talk session
// (the same shared-Result machinery as fights and shops). Lines load:
// docdb narration (the harness's authored hot layer) → the village
// row's payload → the canned pool. A convo's final line may carry a
// quest spec; the grant lands when the talk closes.

// (NarrStore + TalkLine + QuestSpec + Talk live in town.go next to the
// narration key namespace.)

// cannedConvo is the ALWAYS-available pool: role-and-name flavored
// one-liners — the game master degrades gracefully, never blocking.
func cannedConvo(role, npc, town string) []TalkLine {
	var pool []string
	switch role {
	case "innkeep":
		pool = []string{
			fmt.Sprintf("%s wipes the bar — beds are short, coin is shorter.", npc),
			"rest soon, traveler; the dark takes the bold after dark",
		}
	case "storekeep":
		pool = []string{
			fmt.Sprintf("%s taps the counter — \"wares when the doors open\"", npc),
			"no browsing before dawn, outlander",
		}
	default: // villager
		pool = []string{
			fmt.Sprintf("%s measures you with one glance — then points at the world's edge", npc),
			"stay near the lamps",
		}
	}
	lines := make([]TalkLine, len(pool))
	for i, l := range pool {
		lines[i] = TalkLine{Text: l}
	}
	return lines
}

// openTalk materializes the NPC's conversation: docdb narration →
// the authored row payload → the canned pool.
func (s *Server) openTalk(p *Player, t *TownState, i int) {
	npc := t.Dots[i]
	role := npc.Role
	if role == "" {
		role = "villager"
	}
	var lines []TalkLine

	// 1: docdb (the harness's authored lines, override-able hot)
	sk := narrKey(s.worldID(), t.ID, npc.Name)
	if s.narr != nil {
		var doc struct {
			Lines []TalkLine `json:"lines"`
		}
		if err := s.narr.NarrDocByKey(context.Background(), sk, &doc); err == nil && len(doc.Lines) > 0 {
			lines = doc.Lines
		}
	}
	// 2: the authored row payload's npc convo entries
	if len(lines) == 0 {
		lines = t.talkLines(i)
	}
	// 3: the canned pool
	if len(lines) == 0 {
		lines = cannedConvo(role, npc.Name, t.Name)
	}

	if s.talks == nil {
		s.talks = make(map[string]*Talk)
	}
	s.talks[p.Fingerprint] = &Talk{
		TownName: t.Name, Name: npc.Name, Role: role,
		Lines: lines,
	}
	w := s.state
	w.setEvent(p.Fingerprint, fmt.Sprintf("%s looks at you — [enter] to listen", npc.Name))
}

// advanceTalk: enter advances the shown line; once the convo runs dry
// the session closes and any final-line quest grants.
func (s *Server) advanceTalk(p *Player) {
	talk := s.talks[p.Fingerprint]
	if talk == nil {
		return
	}
	talk.Line++
	if talk.Line < len(talk.Lines) {
		return // the next line shows
	}
	// the convo ends: grant the tail quest if the last line carried one
	if last := talk.Lines[len(talk.Lines)-1]; last.Quest != nil {
		s.grantQuest(p, last.Quest)
	}
	delete(s.talks, p.Fingerprint)
	w := s.state
	w.setEvent(p.Fingerprint, fmt.Sprintf("%s nods — the talk ends", talk.Name))
}

// grantQuest: one active copy per title (the giver's line is the
// source; re-talk refreshes nothing yet — slice 6's policy).
func (s *Server) grantQuest(p *Player, q *QuestSpec) {
	for _, have := range p.quests {
		if have.Title == q.Title {
			s.state.setEvent(p.Fingerprint, fmt.Sprintf("%q still stands", q.Title))
			return
		}
	}
	need := q.Count
	if need < 1 {
		need = 1
	}
	p.quests = append(p.quests, Quest{
		Title: q.Title, Objective: q.Objective, Target: q.Target,
		Need: need, Coins: q.RewardCoins, XP: q.RewardXP,
	})
	s.state.setEvent(p.Fingerprint, fmt.Sprintf("%q — task accepted: %s the %s (x%d)",
		q.Title, verbFor(q.Objective), q.Target, need))
	slog.Info("quest granted", "title", q.Title, "fp", p.Fingerprint)
}

// questKill: one kill of a named enemy group jumps every matching
// bounty's progress by one; the last one pays out.
func (s *Server) questKill(p *Player, enemyName string) {
	w := s.state
	for i := range p.quests {
		q := &p.quests[i]
		if q.Done || q.Target != enemyName {
			continue
		}
		q.Done = q.Have+1 >= q.Need
		q.Have++
		if !q.Done {
			w.setEvent(p.Fingerprint, fmt.Sprintf("%s: %d/%d", q.Title, q.Have, q.Need))
			continue
		}
		p.Coins += q.Coins
		p.XP += q.XP
		var events []string
		p.checkLevel(w.rnd, &events)
		for _, e := range events {
			w.setEvent(p.Fingerprint, e)
		}
		w.setEvent(p.Fingerprint, fmt.Sprintf("%s COMPLETE — %d coins, %d xp", q.Title, q.Coins, q.XP))
		slog.Info("quest completed", "title", q.Title, "fp", p.Fingerprint)
	}
}

// talkLines looks the roster entry's authored convo up in the town's
// per-NPC map (populated by buildTown from the row's NPC payload).
func (t *TownState) talkLines(i int) []TalkLine {
	if t.NPCConvo == nil || i >= len(t.Dots) {
		return nil
	}
	return t.NPCConvo[t.Dots[i].Name]
}

// verbFor shapes the acceptance log ("slay" beats "resolve").
func verbFor(objective string) string {
	if objective == "kill" {
		return "slay"
	}
	return "resolve"
}

// Quest is one player-tracked bounty granted by a talk's final line.
type Quest struct {
	Title     string
	Objective string // "kill"
	Target    string // the enemy group name
	Need      int
	Have      int
	Done      bool
	Coins     int
	XP        int
}

var _ = time.Now
