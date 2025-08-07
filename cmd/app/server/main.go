package main

import (
	"context"
	"fmt"
	"time"
)
// this is the main file for now this service is structured like this 

// still deciding on how we are structuring this service for right now 
// we should write components functions to call as go routines in the main function
// right now i just have this here 
// we might need to consider writing multiple functions for multiple go routines for processes that block
// other processes

// here is an example function on how where we are are doing stuff to run in our main function 
func mainloop(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <- ctx.Done():
			fmt.Printf("stopping task: %v\n", ctx.Err())
			return
		case <- ticker.C:
			fmt.Println("server is currently running")
		}
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println("starting  server")
	go mainloop(ctx) // we call it as a go routine

	<- ctx.Done()
	// for testing purposes
	time.Sleep(50 * time.Millisecond)
}
