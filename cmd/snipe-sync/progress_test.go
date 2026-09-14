package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestFailedReconciliationRetainsMeasuredResults(t *testing.T) {
	var rows, logs bytes.Buffer
	o := &commandOutput{out: &rows, interactive: true}
	logger := slog.New(&stageHandler{Handler: slog.NewTextHandler(&logs, nil), output: o})
	logger.Info("Fetching inventory", "stage", true)
	for current := range 4 {
		logger.Info("Fetching inventory", "progress", true, "current", current, "total", 3, "unit", "sources")
	}
	logger.Info("Fetching inventory", "stage_result", true)
	logger.Info("Planning changes", "stage", true)
	logger.Info("Planning changes", "stage_result", true, "error", errors.New("policy failed"))
	logger.Info("Provider notice")
	o.endProgress(errors.New("policy failed"))
	text := rows.String()
	for _, want := range []string{"✗ Users and assets  failed", "    ✓ Fetching inventory", "3/3 sources 100%", "    ✗ Planning changes"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q: %s", want, text)
		}
	}
	if strings.Count(text, "Users and assets") != 1 || strings.Count(text, "Fetching inventory") != 1 || strings.Contains(text, "(0s)") || strings.Contains(text, "·") {
		t.Fatalf("duplicate or noisy rows: %s", text)
	}
	if strings.Contains(logs.String(), "Fetching inventory") || !strings.Contains(logs.String(), "Provider notice") {
		t.Fatalf("diagnostics: %s", logs.String())
	}
}

func TestInterruptedOperationDoesNotClaimSuccess(t *testing.T) {
	var out bytes.Buffer
	o := &commandOutput{out: &out, interactive: true}
	o.progress = newTerminalProgress(&out)
	o.progress.update(activity{label: "Reading inventory", stage: true})
	o.progress.update(activity{current: 10, total: -1, unit: "records", progress: true})
	o.endProgress(context.Canceled)
	if strings.Contains(out.String(), "%") || !strings.Contains(out.String(), "10 records") || !strings.Contains(out.String(), "interrupted") || strings.Contains(out.String(), "✓") {
		t.Fatalf("unknown progress: %s", out.String())
	}
}

func TestProgressLogVolumeRespectsVerbosity(t *testing.T) {
	for _, level := range []slog.Level{slog.LevelInfo, slog.LevelDebug} {
		var out bytes.Buffer
		o := &commandOutput{out: &out}
		logger := slog.New(&stageHandler{Handler: slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: level}), output: o})
		for current := range 10 {
			logger.Info("Transfer progress", "progress", true, "current", current, "total", 9, "unit", "bytes", "progress_final", current == 9)
		}
		want := 1
		if level == slog.LevelDebug {
			want = 10
		}
		if got := strings.Count(out.String(), "Transfer progress"); got != want {
			t.Fatalf("level %s: %d logs, want %d", level, got, want)
		}
	}
}

func TestSuccessfulReconciliationCollapses(t *testing.T) {
	var out bytes.Buffer
	p := newTerminalProgress(&out)
	p.update(activity{label: "Planning changes", stage: true})
	p.update(activity{label: "Planning changes", status: true})
	p.stop("")
	if strings.Contains(out.String(), "Planning changes") || strings.Count(strings.TrimSpace(out.String()), "\n") != 0 || !strings.Contains(out.String(), "✓") {
		t.Fatalf("successful run: %s", out.String())
	}
}
