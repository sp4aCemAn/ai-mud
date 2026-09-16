package game

import (
	"fmt"
	"strings"
)

// Shop is the UI-facing alias (Result.Shop keeps its name).
type Shop = Store

// The store (slice 5). ONE shape and ONE shared buy path serves both
// entrances: the wander merchant's cart and a town's storekeeper.
// Sessions are keyed by the STORE (public areas — many players browse
// the same counter at once; buys are per-player), each player holds
// one browsing ref at a time.

// ShopItem is one row on the store overlay; Ref resolves the registry.
type ShopItem struct {
	Name  string // what it is
	Desc  string // what it does
	Price int    // in coin
	Ref   string // the ItemSpec id ("potion:dark" | "ashfen:wardmail")
}

// Store is the UI-facing store snapshot (renamed from the old
// per-player machinery; the wander store is just one instance).
type Store struct {
	Keeper string     // vendor name for the dialog line
	Town   string     // "" = the wander cart
	Pitch  string     // the vendor's dialog line (authored flavor)
	Lines  []ShopItem // [1..n] buyables
}

// merchantName — the wander peddler.
const merchantName = "Old Marren"

// wanderStoreKey and townStoreKey are the session keys. Stores are
// public: bumping the same counter from different fingerprints mounts
// the SAME Store session.
const wanderStoreKey = "wander:" + merchantName

func townStoreKey(townID int64, keeper string) string {
	return fmt.Sprintf("town:%d:%s", townID, keeper)
}

// wanderStore builds the peddler's cart from the global registry —
// the same goods the old hardcoded table listed, by ref.
func wanderStore() *Store {
	return &Store{
		Keeper: merchantName,
		Pitch:  "Cold night for wandering... coin buys warmth.",
		Lines:  shopLinesFrom([]string{"potion:dark", "draught:mana", "sword:blackiron", "shroud:ursa"}),
	}
}

// newMerchant places the wander vendor on a main-region tile not too
// near spawn — reachable, and never on top of an enemy dot.
func newMerchant(w *worldState, sx, sy int) Dot {
	x, y := w.pickRegionSpot(sx, sy, 3)
	return Dot{X: x, Y: y, Kind: "npc", Count: 1, Name: merchantName}
}

// shopDisplay renders the vendor's opening line. The purse is the
// player's (per-player decor over the SHARED store).
func (sh *Store) pitchFor(p *Player) *Shop {
	cp := *sh
	cp.Pitch = fmt.Sprintf("%s (you carry %dc)", sh.Pitch, p.Coins)
	return &cp
}

// buy resolves one purchase off a counter: registry-ref semantics, the
// shared vocabulary on failure (the headline voice is the counter's
// KEEPER, never a dead end). Assumes the caller holds the players lock.
func (sh *Store) buyFrom(w *worldState, p *Player, item int) []string {
	if sh == nil {
		return []string{"no counter is open to buy from."}
	}
	if item < 1 || item > len(sh.Lines) {
		return []string{fmt.Sprintf("%s glances around: \"that shelf is empty.\"", sh.Keeper)}
	}
	ware := sh.Lines[item-1]
	spec, ok := itemSpec(ware.Ref)
	if !ok {
		return []string{fmt.Sprintf("%s squints at the shelf: \"restock incoming.\"",
			sh.Keeper)}
	}
	if p.Coins < ware.Price {
		return []string{fmt.Sprintf("%s squints at your purse: \"not enough coin.\"", sh.Keeper)}
	}
	p.Coins -= ware.Price

	if spec.Kind == "potion" {
		p.Inventory[spec.Inv]++
		return []string{fmt.Sprintf("%s wraps %s in cloth. Use it from the pack [i].",
			sh.Keeper, spec.Name)}
	}
	// gear: the effect lands immediately (the registry is the writer)
	eff := applyEffect(p, spec.Effect)
	if eff == "" {
		// an authored effect the registry can't carry: never silently
		// absorb the coin for gear that does nothing
		p.Coins += ware.Price
		return []string{fmt.Sprintf("%s studies the %s: \"that %s is beyond this counter's smith.\"",
			sh.Keeper, spec.Name, spec.Name)}
	}
	noun, verb := "attack", "hands over"
	if strings.HasPrefix(eff, "def") {
		noun, verb = "defence", "swings"
	}
	return []string{fmt.Sprintf("%s %s the %s (+%s %s).",
		sh.Keeper, verb, spec.Name, eff[len("atk:"):], noun)}
}

// PackPotions is the pack's potion order — DERIVED from the registry
// (global list order, kind=potion; one authority, no hidden shadow
// list). useItem resolves by this order; the pack overlay hints with
// the same list's display names.
func PackPotions() []ItemSpec {
	var out []ItemSpec
	for _, it := range globalItems {
		if it.Kind == "potion" {
			out = append(out, it)
		}
	}
	return out
}

// useItem applies pack items: arg indexes the pack's potion order
// (the registry drives it — kind=potion in global-item order).
func useItem(p *Player, arg int) []string {
	var potions []string
	for _, it := range PackPotions() {
		potions = append(potions, it.ID)
	}
	if arg < 1 || arg > len(potions) {
		return []string{"nothing like that in the pack"}
	}
	spec, ok := itemSpec(potions[arg-1])
	if !ok {
		return []string{"nothing like that in the pack"}
	}
	if p.Inventory[spec.Inv] < 1 {
		return []string{fmt.Sprintf("no %s in the pack", spec.Name)}
	}
	p.Inventory[spec.Inv]--
	switch spec.Effect {
	case "hp:+8":
		p.HP = min(p.HP+8, p.MaxHP)
		return []string{"the potion is bitter — wounds close (hp restored)"}
	case "mana:+4":
		p.Mana = min(p.Mana+4, p.MaxMana)
		return []string{"the draught blurs the air — mana flows back"}
	}
	return []string{"nothing happens"}
}
