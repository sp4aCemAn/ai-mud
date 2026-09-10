package main

import (
	"context"
	"fmt"
	"time"
)

func dosomething(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("stopping task: %v\n", ctx.Err())
			return
		case <-ticker.C:
			fmt.Println("unit_test is currently running")
		}
	}
}

func setupConstructor(ctx context.Context) {
	// this is where we will write unit tests for various stuff
	// right now im writing components for listening over ssh and im testing them here
	// i will also use this to test other various components

}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fmt.Println("starting  server")
	go dosomething(ctx)
	go setupConstructor(ctx) // whatever code we run will run in a go routine
	<-ctx.Done()
	// for testing purposes
	time.Sleep(50 * time.Millisecond)
}
