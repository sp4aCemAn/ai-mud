package game

import (
	"fmt"
)

// The merchant. One dot on the map; walking into it opens the store.
// Vendor turn is a stub: Old Marren has nothing of interest for now —
// the AI harness will own the conversations later, the transaction
// mechanics below stay as-is.

// ShopItem is one row in the store overlay.
type ShopItem struct {
	Name  string // what it is
	Desc  string // what it does
	Price int    // in coin
}

// Shop is the UI-facing store snapshot.
type Shop struct {
	Keeper string     // vendor name for the dialog line
	Pitch  string     // the dialog (dev line for now)
	Lines  []ShopItem // [1..n] buyables
}

// merchantName — the peddler.
const merchantName = "Old Marren"

// wares is the persistent catalog. Indices are the buy keys.
var wares = []ShopItem{
	{Name: "dark potions", Desc: "mends 8 health", Price: 12},
	{Name: "mana draughts", Desc: "eases 4 mana", Price: 10},
	{Name: "black iron sword", Desc: "+2 to every slash", Price: 40},
	{Name: "ursa shroud", Desc: "+1 beats off a blow", Price: 35},
}

// newMerchant places the vendor on a main-region tile not too near
// spawn — reachable, and never on top of an enemy dot.
func newMerchant(w *worldState, sx, sy int) Dot {
	x, y := w.pickRegionSpot(sx, sy, 3)
	return Dot{X: x, Y: y, Kind: "npc", Count: 1, Name: merchantName}
}

// shopOpen builds the store snapshot.
func shopOpen(p *Player) *Shop {
	s := &Shop{
		Keeper: merchantName,
		Pitch:  fmt.Sprintf("Cold night for wandering, %s. Coin buys warmth. (you carry %dc)", p.Name, p.Coins),
		Lines:  wares,
	}
	return s
}

// buy resolves a purchase: coins, stat perks, inventory count. Failing
// buys produce a vendor line, never a dead end — the overlay stays put.
func buy(w *worldState, p *Player, item int) []string {
	if item < 1 || item > len(wares) {
		return []string{fmt.Sprintf("%s glances around: 'that shelf is empty.'", merchantName)}
	}
	ware := wares[item-1]
	if p.Coins < ware.Price {
		return []string{fmt.Sprintf("%s squints at your purse: 'not enough coin.'", merchantName)}
	}
	p.Coins -= ware.Price

	switch item {
	case 1:
		p.Inventory["dark potions"]++
		return []string{fmt.Sprintf("%s wraps a dark potion in cloth. Use it from the pack [i].", merchantName)}
	case 2:
		p.Inventory["mana draughts"]++
		return []string{fmt.Sprintf("%s pours sloe-purple draught into a flask. Use it from the pack [i].", merchantName)}
	case 3:
		p.Atk += 2
		return []string{fmt.Sprintf("%s hands over the black iron sword. (+2 attack)", merchantName)}
	case 4:
		p.Def += 1
		return []string{fmt.Sprintf("%s swings the ursa shroud over your shoulders. (+1 defence)", merchantName)}
	}
	return nil
}
