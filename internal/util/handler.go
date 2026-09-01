package util

import (
	"context"
	"log/slog"
)

// NOTES: we only ErrorHandler in main loop because that's where we
// manage heartbeat, whenever we have an error funnel it to main
// otherwise we call WarningHandler

// for later
type status struct {
	healty bool
}

// ErrorHandler is for unrecoverable errors: log it, then block until the
// context is cancelled (which starts the shutdown path).
func ErrorHandler(err error, message string, ctx context.Context) {
	if err != nil {
		slog.Error(message, "err", err)
		<-ctx.Done()
	}
}

// experiment with this later
func HandleError(err error, message string, callback func(), ctx context.Context) {
	if err != nil {
		if callback != nil {
			callback()
		}
		WarningHandler(err, message)
		<-ctx.Done()
	}
}

// this function is only called when an error occurs
// NOTE: log.Panicf kills the process — use slog.Warn unless the error
// really is fatal
func WarningHandler(err error, message string) {
	slog.Warn(message, "err", err)
}

// if not healthy do something about it
func checkHealth(err error, message string, healty status) {
	// unimplemented
}
