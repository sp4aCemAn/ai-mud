package util

import (
	"context"
	"log"
)

// for later 
type status struct {
healty bool;
}

func ErrorHandler(err error, message string, ctx context.Context) {
	if err != nil {
		log.Fatalf("%s: %v", err, message)
		<-ctx.Done()
	}
}

// for when we dont want to stop the service when something fails
// for debug
func WarningHandler(err error, message string) {
	if err != nil {
		log.Panicf("%s: %v", err, message)

	}
}

// if not healthy do something about it
func checkHealth (err error, message string, healty status) {
	// unimplemented
	
}


