package unit_test

import (
	ui "github.com/sp4aCemAn/ai-mud/internal/ui"
	"fmt"
	"testing"
  "os"

	tea "github.com/charmbracelet/bubbletea"
)


func TestBasicUi(t *testing.T) {
	fmt.Println("Running TestBasicUi...")
  initalModel := ui.InitalScreen()
  p:=tea.NewProgram(initalModel)
  
  if _, err := p.Run(); err != nil {
		// If there's an error, print it to the console.
		fmt.Printf("Oh no! There was an error: %v\n", err)
		// Exit with a non-zero status code to indicate an error.
		os.Exit(1)
	}

	// You can use t.Log for output that only shows on failure or with -v flag
	t.Log("Test log message.")

	// You can use t.Error, t.Fail, t.Errorf to signal test failures
	// if someConditionFails {
	//  t.Errorf("Something went wrong, expected X got Y")
	// }

	fmt.Println("TestBasicUi finished.")
}


