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
		case <- ticker.C:
		fmt.Println("unit_test is currently running")

		}

	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fmt.Println("starting  server")
	go dosomething(ctx)

	<- ctx.Done()
	// for testing purposes
	time.Sleep(50 * time.Millisecond)
}
