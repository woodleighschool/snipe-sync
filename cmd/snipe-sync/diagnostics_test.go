package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestFiniteDiagnosticsKeepWarningsWithoutStageLogs(t *testing.T) {
	command, output := newRootCommand()
	var report, diagnostics bytes.Buffer
	command.SetOut(&report)
	command.SetErr(&diagnostics)
	command.AddCommand(&cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		output.logger.InfoContext(cmd.Context(), "Fetching inventory", "stage", true)
		output.logger.InfoContext(cmd.Context(), "Fetching inventory", "progress", true, "current", 1)
		output.logger.InfoContext(cmd.Context(), "Fetching inventory", "stage_result", true)
		output.logger.Debug("Request detail")
		output.logger.Warn("Partial inventory")
		output.logger.Warn("Reported failure", "report_detail", true)
		return errors.New("source failed:\n\tHTTP 503")
	}})
	command.SetArgs([]string{"probe"})
	executed, err := command.ExecuteC()
	output.finish(executed, err)
	if err == nil {
		t.Fatal("probe succeeded")
	}
	if got, want := diagnostics.String(), "Warning: Partial inventory\nError: source failed: HTTP 503\n"; got != want {
		t.Fatalf("diagnostics = %q, want %q", got, want)
	}
	if report.Len() != 0 {
		t.Fatalf("fabricated report: %s", &report)
	}
}

func TestRunUsesJSONLogsAndConfiguredLevel(t *testing.T) {
	for _, level := range []string{"", "debug"} {
		t.Run(level, func(t *testing.T) {
			t.Setenv("SNIPE_SYNC_LOG_LEVEL", "warn")
			path := writeCommandConfig(t, t.TempDir(), "config.yaml", commandConfig)
			command, output := newRootCommand()
			var report, diagnostics bytes.Buffer
			command.SetOut(&report)
			command.SetErr(&diagnostics)
			run, _, err := command.Find([]string{"run"})
			if err != nil {
				t.Fatal(err)
			}
			run.RunE = func(cmd *cobra.Command, _ []string) error {
				if _, err := (&cli{configPaths: []string{path}, output: output}).loadConfig(cmd); err != nil {
					return err
				}
				output.logger.Info("Fetching inventory", "stage", true)
				output.logger.Warn("Provider warning")
				return nil
			}
			args := []string{"run"}
			if level != "" {
				args = append(args, "--log-level", level)
			}
			command.SetArgs(args)
			executed, err := command.ExecuteC()
			output.finish(executed, err)
			if err != nil {
				t.Fatal(err)
			}
			if report.Len() != 0 {
				t.Fatalf("daemon report: %s", &report)
			}
			if strings.Contains(diagnostics.String(), "Fetching inventory") != (level == "debug") {
				t.Fatalf("daemon stage level: %s", &diagnostics)
			}
			decoder := json.NewDecoder(&diagnostics)
			warnings := 0
			for {
				var record struct {
					Message string `json:"msg"`
					Level   string `json:"level"`
				}
				if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if record.Message == "Fetching inventory" && record.Level != "DEBUG" {
					t.Fatalf("stage level = %s", record.Level)
				}
				if record.Message == "Provider warning" {
					warnings++
				}
			}
			if warnings != 1 {
				t.Fatalf("warnings = %d", warnings)
			}
		})
	}
}

func TestRunHelpDoesNotLogShutdown(t *testing.T) {
	command, output := newRootCommand()
	var report, diagnostics bytes.Buffer
	command.SetOut(&report)
	command.SetErr(&diagnostics)
	command.SetArgs([]string{"run", "--help"})
	executed, err := command.ExecuteC()
	output.finish(executed, err)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "Reconcile immediately") || diagnostics.Len() != 0 {
		t.Fatalf("help = %q, diagnostics = %q", &report, &diagnostics)
	}
}

func TestRemovedFlagsAndFiniteLogLevelAreRejected(t *testing.T) {
	for _, commandName := range []string{"plan", "apply", "validate", "run"} {
		flags := [][]string{{"--output", "json"}, {"--log-format", "json"}, {"--quiet"}, {"-q"}, {"--verbose"}, {"-v"}, {"--debug"}}
		if commandName != "run" {
			flags = append(flags, []string{"--log-level", "debug"})
		}
		for _, flag := range flags {
			command, _ := newRootCommand()
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(append([]string{commandName}, flag...))
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "unknown") {
				t.Errorf("%s %v: error = %v", commandName, flag, err)
			}
		}
	}
}

func TestEarlyJSONFailureDoesNotFabricateReport(t *testing.T) {
	command, output := newRootCommand()
	var report, diagnostics bytes.Buffer
	command.SetOut(&report)
	command.SetErr(&diagnostics)
	command.SetArgs([]string{"plan", "--config", "missing.yaml", "--json"})
	executed, err := command.ExecuteC()
	output.finish(executed, err)
	if err == nil {
		t.Fatal("expected failure")
	}
	if report.Len() != 0 || strings.Count(diagnostics.String(), "Error:") != 1 {
		t.Fatalf("stdout = %s, stderr = %s", &report, &diagnostics)
	}
}

func TestFiniteWarningsIgnoreDaemonLogLevel(t *testing.T) {
	t.Setenv("SNIPE_SYNC_LOG_LEVEL", "error")
	path := writeCommandConfig(t, t.TempDir(), "config.yaml", commandConfig)
	command, output := newRootCommand()
	var diagnostics bytes.Buffer
	command.SetOut(io.Discard)
	command.SetErr(&diagnostics)
	command.AddCommand(&cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
		if _, err := (&cli{configPaths: []string{path}, output: output}).loadConfig(cmd); err != nil {
			return err
		}
		output.logger.Warn("Enrichment unavailable")
		return nil
	}})
	command.SetArgs([]string{"probe"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if diagnostics.String() != "Warning: Enrichment unavailable\n" {
		t.Fatalf("warnings: %s", &diagnostics)
	}
}

func TestDurableStdoutPermanentlyStopsProgress(t *testing.T) {
	command, output := newRootCommand()
	var report, stderr bytes.Buffer
	command.SetOut(&report)
	command.SetErr(&stderr)
	if err := output.start(command); err != nil {
		t.Fatal(err)
	}
	output.interactive = true
	output.logger.Info("Reading inventory", "stage", true)
	if output.progress == nil {
		t.Fatal("test did not begin progress")
	}
	if _, err := io.WriteString(command.OutOrStdout(), "Report\n"); err != nil {
		t.Fatal(err)
	}
	output.logger.Info("Late provider activity", "stage", true)
	if output.interactive || output.progress != nil || report.String() != "Report\n" || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q, progress = %v", &report, &stderr, output.progress)
	}
}

func TestCancellationIsNotAutomaticallyAnInterrupt(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		cmd, output := newRootCommand()
		var logs bytes.Buffer
		cmd.SetErr(&logs)
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		cmd.SetContext(ctx)
		if interrupted {
			cancel(errInterrupted)
		}
		output.finish(cmd, context.Canceled)
		if strings.Contains(logs.String(), "interrupted") != interrupted {
			t.Fatalf("interrupted=%v: %s", interrupted, logs.String())
		}
	}
}
