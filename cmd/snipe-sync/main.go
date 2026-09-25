package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

var errInterrupted = errors.New("interrupted")
var errTerminated = errors.New("terminated")

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signalContext()
	defer stop()
	command, output := newRootCommand()
	executed, err := command.ExecuteContextC(ctx)
	output.finish(executed, err)
	if errors.Is(context.Cause(ctx), errInterrupted) {
		return 130
	}
	if errors.Is(context.Cause(ctx), errTerminated) {
		return 143
	}
	if err != nil {
		return 1
	}
	return 0
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancelCause(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case received := <-signals:
			// Restore the OS action before publishing cancellation: a second
			// interrupt can exit even if cleanup or a native call is blocked.
			signal.Stop(signals)
			cause := errInterrupted
			if received == syscall.SIGTERM {
				cause = errTerminated
			}
			cancel(cause)
		case <-ctx.Done():
			signal.Stop(signals)
		}
	}()
	return ctx, func() { cancel(context.Canceled) }
}
