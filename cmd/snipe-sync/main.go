package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	command, output := newRootCommand()
	executed, err := command.ExecuteContextC(ctx)
	output.finish(executed, err)
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if err != nil {
		return 1
	}
	return 0
}
