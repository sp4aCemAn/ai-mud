package tui

import (
  "fmt"
)


// a pane is just a screen in which things are "rendered" to
// make panes have windows
// update requires current panes
// you have to create a pane like creating a canvas to display dynamic obj 
// you can name panes or put text to parent  "window" that is associated to pane 
type WindowHandler interface {
  update_pane() //called by pane to update window
  // window will loop through and display 2d pointer of char and then display a name (if theres one) and (discr) if theres one
}

type Pane struct {
  name string // name pane
  discr string // used for possibly displaying stats or information about whats going on
  // figure out 2d array of pointers of char 
}

type PaneHandler interface { 
  update() // update state and call parent to display to screen needs to be async takes a list of pointers 
  get_diff() // we only want to change the different char of each pane so compare and get only the different char and return a list of pointers or something
}



type Window struct {
  panes map[string]*Pane


}


type Player struct {
  x int16
  y int16

}

type Playerer interface {
  move()
}





