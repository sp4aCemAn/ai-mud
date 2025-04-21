package ui

import (
  "fmt"
  "strings"

  tea "github.com/charmbracelet/bubbletea"
  "github.com/charmbracelet/lipgloss"
)





type model struct {
  termWidth int
  termHeight int
  text string
  boxStyle lipgloss.Style

}


func InitalModel() model {
  baseStyle:= lipgloss.NewStyle().
      Border(lipgloss.NormalBorder()).
      BorderBackground(lipgloss.Color("124")).
      Padding(1,1).
      Align(lipgloss.Center)

  return model {
    text: "Hello world",
    boxStyle: baseStyle,

  }

}

func (m model) Init() tea.Cmd {
  return  nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
  switch msg := msg.(type) {
  // key msgs
  case tea.KeyMsg:
    switch msg.String() {
    case "q":
      return m, tea.Quit
    } 

  case tea.WindowSizeMsg:
    m.termWidth = msg.Width
   m.termHeight = msg.Height
  } 

  return m, nil
}


func (m model) View() string {


  if m.termHeight ==0 {
    fmt.Print("init...")
  }
  boxWidth := m.termWidth - 2
  boxHeight := m.termHeight -2
  if boxWidth < 0 { boxWidth = 0 }
  if boxHeight < 0 { boxHeight = 0 }

  style := m.boxStyle 

  style = m.boxStyle.Width(boxWidth).Height(boxHeight)

  var b strings.Builder
  style.Render(b.String())

  



  test :=  style.Render(m.text)
  fullView := lipgloss.Place(
    m.termWidth,
    m.termHeight,
    lipgloss.Center,
    lipgloss.Center,
    test,
    )
  return fullView
}



