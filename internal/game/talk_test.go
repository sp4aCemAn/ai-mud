package game

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/storage"
)

func townRowWithConvo(t *testing.T, id int64, name string) storage.WorldObject {
	t.Helper()
	return storage.WorldObject{
		ID: id, Kind: storage.ObjectVillage, Name: name,
		HomeX: 50, HomeY: 20, Radius: 4,
		Payload: json.RawMessage(`{"npcs":[
			{"type":"villager","name":"Rosie","x":3,"y":2,
			 "convo":[{"text":"the lamps died at the crossroads three nights back"},
			          {"text":"a bounty would see them raised",
			           "quest":{"title":"the lamps of rosie","objective":"kill",
			                    "target":"test wights","count":2,
			                    "reward_coins":20,"reward_xp":30}}]},
			{"type":"villager","name":"Chatter","x":5,"y":2}
		]}`),
	}
}

func enterTalkTown(t *testing.T) (*Server, *Player, *TownState) {
	t.Helper()
	s := NewServerWorld(WorldSpec{Name: "talkville", Seed: 20260909, WW: WorldW, WH: WorldH})
	s.Join("fp", "guest")
	row := townRowWithConvo(t, 7, "talkville")
	s.registerTown(row)
	s.enterTown(s.players["fp"], 7)
	tc := s.townOrBuild(7)
	return s, s.players["fp"], tc
}

func TestTalkFlowCannedFallback(t *testing.T) {
	s, _, tc := enterTalkTown(t)
	// Chatter has NO authored convo: the canned pool speaks
	for i := range tc.Dots {
		if tc.Dots[i].Name == "Chatter" {
			s.openTalk(s.players["fp"], tc, i)
			break
		}
	}
	talk := s.talks["fp"]
	if talk == nil || len(talk.Lines) == 0 {
		t.Fatalf("canned talk missing: %+v", talk)
	}
	if talk.Role != "villager" || talk.TownName != "talkville" {
		t.Fatalf("talk shape: %+v", talk)
	}
	// enter through the lines: the session closes; no quest rides it
	s.advanceTalk(s.players["fp"])
	if s.talks["fp"] == nil {
		t.Fatal("multi-line convo should still be open right after line 1")
	}
	s.advanceTalk(s.players["fp"])
	if s.talks["fp"] != nil {
		t.Fatal("the convo should close once the lines run out")
	}
	if len(s.players["fp"].quests) != 0 {
		t.Fatalf("no quest rides Chatter: %+v", s.players["fp"].quests)
	}
}

func TestTalkPayloadConvoGrantsQuest(t *testing.T) {
	s, p, tc := enterTalkTown(t)
	for i := range tc.Dots {
		if tc.Dots[i].Name == "Rosie" {
			s.openTalk(p, tc, i)
			break
		}
	}
	// line 1 shows; entering again GRANTS the authored quest
	s.advanceTalk(p)
	talk := s.talks["fp"]
	if talk == nil || len(p.quests) != 0 {
		t.Fatalf("line 1 should show the second line's talk: %+v talk=%+v", p.quests, talk)
	}
	s.advanceTalk(p)
	if s.talks["fp"] != nil {
		t.Fatal("the convo should lance the quest and close")
	}
	if len(p.quests) != 1 || p.quests[0].Title != "the lamps of rosie" || p.quests[0].Need != 2 {
		t.Fatalf("quest grant: %+v", p.quests)
	}
}

func TestQuestKillTracksAndCompletes(t *testing.T) {
	s, p, _ := enterTalkTown(t)
	s.grantQuest(p, &QuestSpec{Title: "the lamps", Objective: "kill",
		Target: "test wights", Count: 2, RewardCoins: 20, RewardXP: 30})
	s.questKill(p, "test wights")
	if p.quests[0].Done || p.quests[0].Have != 1 {
		t.Fatalf("progress: %+v", p.quests[0])
	}
	coinsBefore := p.Coins
	s.questKill(p, "test wights")
	if !p.quests[0].Done {
		t.Fatal("the bounty should complete on the second kill")
	}
	if p.Coins != coinsBefore+20 {
		t.Fatalf("reward: %d (want +%d)", p.Coins, 20)
	}
	if p.XP == 0 {
		t.Fatal("xp reward missing")
	}
	// non-target kills count nothing
	before := p.quests[0].Have
	s.questKill(p, "carrion ghouls")
	if p.quests[0].Have != before {
		t.Fatal("a stray kill jumped the bounty")
	}
}

// fakeNarrStore: the docdb layer overrides the payload's lines.
type fakeNarrStore struct{ docs map[string]string }

func (f *fakeNarrStore) PutNarrDoc(_ context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.docs[key] = string(b)
	return nil
}

func (f *fakeNarrStore) NarrDocByKey(_ context.Context, key string, v any) error {
	raw, ok := f.docs[key]
	if !ok {
		return storage.ErrNotFound
	}
	return json.Unmarshal([]byte(raw), v)
}

func TestTalkDocdbOverridesPayload(t *testing.T) {
	s, _, tc := enterTalkTown(t)
	narr := &fakeNarrStore{docs: map[string]string{}}
	s.AttachNarrStore(narr)
	s.AttachNarrStore(s.narr) // idempotent re-attach stays put

	_ = narr.PutNarrDoc(context.Background(),
		narrKey(s.worldID(), 7, "Rosie"),
		map[string]any{"lines": []map[string]any{
			{"text": "narr display pattern: the harness wrote this"},
		}})
	for i := range tc.Dots {
		if tc.Dots[i].Name == "Rosie" {
			s.openTalk(s.players["fp"], tc, i)
			break
		}
	}
	talk := s.talks["fp"]
	if talk == nil || len(talk.Lines) != 1 ||
		talk.Lines[0].Text != "narr display pattern: the harness wrote this" {
		t.Fatalf("docdb narration must win: %+v", talk)
	}
}
