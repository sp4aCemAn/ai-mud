package util

import (
	"context"
	"log"
)
// NOTES: we only ErrorHandler in main loop because that's where we 
// manage heartbeat, whenever we have an error funnel it to main
// otherwise we call WarningHandler

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

// experiment with this later
func HandleError(err error,message string, callback func(),ctx context.Context){
	if err != nil {
		callback()
		WarningHandler(err ,message)
		<- ctx.Done()
	}
}

// this function is only called when an error occurs 
func WarningHandler(err error, message string) {
		log.Panicf("%s: %v", err, message)
}

// if not healthy do something about it
func checkHealth (err error, message string, healty status) {
	// unimplemented
	
}


