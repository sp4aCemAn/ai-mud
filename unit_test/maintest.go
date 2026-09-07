package unit_test

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sp4aceman/ai-mud/internal/auth"
	"github.com/sp4aceman/ai-mud/internal/ui"
)

func main() {
	fmt.Println("Running main...")
	identity := auth.Identity{User: auth.User{ID: "guest", Name: "guest"}}
	initialModel := ui.NewRouter(identity, nil)
	p := tea.NewProgram(initialModel)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Oh no! There was an error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("TestBasicUi finished.")
}
