package util

import (
	"context"
	"log"
)


func ErrorHandler(err error, message string, ctx context.Context) {
	if err != nil {
		log.Fatalf("%s: %v", err, message)
		<-ctx.Done()
	}
}
