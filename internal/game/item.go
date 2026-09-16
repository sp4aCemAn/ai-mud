package game

import (
	"strconv"
	"strings"
)

// The item registry (slice 5). ONE shape for every item in the game;
// the global list covers the world's evergreen goods, a village row's
// data.items may declare town-local ones (IDs prefix-qualified by the
// town name — the no-collision rule). Wares on any store counter are
// REFERENCES into a scope, never inline effect blobs.

// ItemSpec is the canonical item definition.
type ItemSpec struct {
	ID     string `json:"id"` // "potion:dark" | "ashfen:wardmail"
	Name   string `json:"name"`
	Desc   string `json:"desc"`
	Price  int    `json:"price"`
	Kind   string `json:"kind"`   // "potion" (pack-applied) | "gear" (immediate)
	Effect string `json:"effect"` // "stat:+n" — resolved for gear buys
	Inv    string `json:"inv"`    // the pack's inventory key (potions)
}

// globalItems: the wander store's stock and the fallback for unknown
// counters. The live catalog first (the four Old Marren goods), ids
// stable — the display entries are materialized into ShopItem lines.
var globalItems = []ItemSpec{
	{ID: "potion:dark", Name: "dark potions", Desc: "mends 8 health", Price: 12,
		Kind: "potion", Effect: "hp:+8", Inv: "dark potions"},
	{ID: "draught:mana", Name: "mana draughts", Desc: "eases 4 mana", Price: 10,
		Kind: "potion", Effect: "mana:+4", Inv: "mana draughts"},
	{ID: "sword:blackiron", Name: "black iron sword", Desc: "+2 to every slash", Price: 40,
		Kind: "gear", Effect: "atk:+2"},
	{ID: "shroud:ursa", Name: "ursa shroud", Desc: "+1 beats off a blow", Price: 35,
		Kind: "gear", Effect: "def:+1"},
}

var catalog map[string]ItemSpec

func initCatalog() {
	catalog = make(map[string]ItemSpec, len(globalItems))
	for _, it := range globalItems {
		catalog[it.ID] = it
	}
}

// itemSpec resolves one ref against the registry. Town-local items are
// merged into the same map by buildTown BEFORE any counter builds —
// the namespace rule (global vs "<town>:" prefixes) guarantees no
// shadowing. Invalid item refs now only fail at tool-write time.
func itemSpec(ref string) (ItemSpec, bool) {
	if catalog == nil {
		initCatalog()
	}
	it, ok := catalog[ref]
	return it, ok
}

// knownItem reports whether a ref resolves — used by buildTown to skip
// unknown authored refs (a GM typo must never block a town) and by the
// local-item validator.
func knownItem(ref string) bool {
	_, ok := itemSpec(ref)
	return ok
}

// isLocalItemID holds the no-collision shape for town-scoped goods:
// "<town>:<thing>". The town prefix comes from the row's own name.
func isLocalItemID(town, id string) bool {
	if town == "" || id == "" {
		return false
	}
	p := town + ":"
	return len(id) > len(p) && id[:len(p)] == p
}

// shopLinesFrom materializes registry refs into the counter's display
// lines (skip-and-warn is the caller's policy; unknown refs never land).
func shopLinesFrom(refs []string) []ShopItem {
	lines := make([]ShopItem, 0, len(refs))
	for _, ref := range refs {
		it, ok := itemSpec(ref)
		if !ok {
			continue
		}
		lines = append(lines, ShopItem{Name: it.Name, Desc: it.Desc, Price: it.Price, Ref: it.ID})
	}
	return lines
}

// applyEffect mutates the player by one spec effect ("stat:+n");
// gear effects land immediately (potions carry their effect onto
// useItem instead). Returns the resolved stat, "" when unparseable.
func applyEffect(p *Player, effect string) string {
	i := strings.IndexByte(effect, ':')
	if i <= 0 {
		return ""
	}
	stat, num := effect[:i], effect[i+1:]
	n, err := strconv.Atoi(num)
	if err != nil || n == 0 {
		return ""
	}
	switch stat {
	case "atk":
		p.Atk += n
	case "def":
		p.Def += n
	default:
		return ""
	}
	return effect
}
