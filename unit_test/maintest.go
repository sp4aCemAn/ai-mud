package unit_test 
import (
	"github.com/sp4aCemAn/ai-mud/internal/ui"
	"fmt"
  "os"
	tea "github.com/charmbracelet/bubbletea"
)
func main() {
	fmt.Println("Running main...")
  initalModel := ui.InitalScreen()
  p:=tea.NewProgram(initalModel)
  
  if _, err := p.Run(); err != nil {
		// If there's an error, print it to the console.
		fmt.Printf("Oh no! There was an error: %v\n", err)
		// Exit with a non-zero status code to indicate an error.
		os.Exit(1)
	}

	// You can use t.Log for output that only shows on failure or with -v flag

	// You can use t.Error, t.Fail, t.Errorf to signal test failures
	// if someConditionFails {
	//  t.Errorf("Something ent wrong, expected X got Y")
	// }

	fmt.Println("TestBasicUi finished.")
}

