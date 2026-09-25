package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestSignalCancelsThenExits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process interrupts are not supported on Windows")
	}
	if os.Getenv("SNIPE_SYNC_SIGNAL_TEST") == "1" {
		ctx, stop := signalContext()
		defer stop()
		fmt.Println("ready")
		<-ctx.Done()
		want := errInterrupted
		if os.Getenv("OUTPUT_TEST_SIGNAL") == syscall.SIGTERM.String() {
			want = errTerminated
		}
		if !errors.Is(context.Cause(ctx), want) {
			t.Fatalf("interrupt cause: %v", context.Cause(ctx))
		}
		fmt.Println("canceled")
		time.Sleep(time.Minute)
		return
	}
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child := exec.CommandContext(ctx, executable, "-test.run=^TestSignalCancelsThenExits$")
			child.Env = append(os.Environ(), "SNIPE_SYNC_SIGNAL_TEST=1", "OUTPUT_TEST_SIGNAL="+sig.String())
			stdout, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = child.Process.Kill() }()
			lines := bufio.NewScanner(stdout)
			for _, want := range []string{"ready", "canceled"} {
				if !lines.Scan() || lines.Text() != want {
					t.Fatalf("child: got %q, want %q", lines.Text(), want)
				}
				if err := child.Process.Signal(sig); err != nil {
					t.Fatal(err)
				}
			}
			if err := child.Wait(); err == nil || ctx.Err() != nil {
				t.Fatalf("second interrupt did not exit promptly: %v, %v", err, ctx.Err())
			}
		})
	}
}
