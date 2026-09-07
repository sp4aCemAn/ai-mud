package unit_test

import (
	"fmt"
	"testing"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/ui"
)

// TestBasicUi launches the full TUI — interactive only, it will wait for
// a terminal. Run manually: go test ./unit_test -run TestBasicUi -v
func TestBasicUi(t *testing.T) {
	t.Skip("interactive TUI test — runs a real terminal program")

	fmt.Println("Running TestBasicUi...")
	identity := auth.Identity{User: auth.User{ID: "guest", Name: "guest"}}
	initialModel := ui.NewRouter(identity)
	_ = initialModel // set a breakpoint / print views here to poke screens

	t.Log("Test log message.")
	fmt.Println("TestBasicUi finished.")
}
