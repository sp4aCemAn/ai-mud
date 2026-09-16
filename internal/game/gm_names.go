package game

import (
	"encoding/json"
	"fmt"
	"time"
)

// The GM's frontier-raising kit: default village rosters and band-name
// flavor for the mediator verbs — the model invents the NAME/WHAT, the
// rails own the roster/aconomy shapes (the AI never invents a
// tradeable world record wholesale; it seeds the mood).

// gmBandNames is the spawn-mediation's name pool (the autoplay bands
// generated the same flavor palette; enemies keep their theme).
var gmBandNames = []string{
	"the ash-shanties", "border ghouls", "the pale hoof",
	"moor-slipping wights", "tin district poachers",
	"the long-house hulks", "farmer's-avId", "the gutter lantern",
}

// gmRosterNames: village folk — a keeper, a storekeep, a villager
// rotate through the pools so two GM-villages never feel cloned.
var gmKeeperNames = []string{"Marda the keeper", "Osric by the fire", "Hulda"}
var gmVendorNames = []string{"Vex", "Wex", "Fenwick the counter", "Brin the stocktaker"}
var gmVillagerNames = []string{"Pell", "Rosie", "Dunna", "Old Thom"}

func pickName(names []string) string {
	if n := len(names); n == 0 {
		return "Sable"
	}
	return names[hashTickJ64()%len(names)]
}

func hashTickJ64() int {
	return int(time.Now().UnixNano() & 0x7fffffff)
}

// defaultVillagePayload: one authored village with a REAL roster — the
// storekeeper sells the global catalog (registry refs since slice 5),
// the innkeep takes the stay, the villager talks (the canned pool
// resolves). The GM names the place; the rails wire the economy.
func defaultVillagePayload() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
		"npcs":[
			{"type":"innkeep","name":%q,"x":3,"y":2},
			{"type":"storekeep","name":%q,"x":4,"y":3,
			 "wares":["potion:dark","draught:mana"]},
			{"type":"villager","name":%q,"x":6,"y":3}
		]}`,
		pickName(gmKeeperNames), pickName(gmVendorNames), pickName(gmVillagerNames)))
}
