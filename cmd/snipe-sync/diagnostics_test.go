package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestLogControlsPreserveReports(t *testing.T) {
	for _, test := range []struct {
		name                  string
		flags                 []string
		stage, debug, warning bool
	}{
		{name: "default", stage: true, warning: true},
		{name: "verbose", flags: []string{"-v"}, stage: true, debug: true, warning: true},
		{name: "debug", flags: []string{"-d"}, stage: true, debug: true, warning: true},
		{name: "quiet", flags: []string{"-q"}, warning: true},
		{name: "error", flags: []string{"--log-level", "error"}},
		{name: "plain", flags: []string{"--no-progress"}, stage: true, warning: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			command, output := newRootCommand()
			var report, diagnostics bytes.Buffer
			command.SetOut(&report)
			command.SetErr(&diagnostics)
			command.AddCommand(&cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
				output.logger.InfoContext(cmd.Context(), "Fetching inventory", "stage", true)
				output.logger.Debug("Request detail")
				output.logger.Warn("Partial inventory")
				_, err := fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
				return err
			}})
			command.SetArgs(append([]string{"probe", "--log-format", "json"}, test.flags...))
			executed, err := command.ExecuteC()
			output.finish(executed, err)
			if err != nil {
				t.Fatal(err)
			}
			if report.String() != "{\"ok\":true}\n" {
				t.Fatalf("report = %q", report.String())
			}
			for message, want := range map[string]bool{"Fetching inventory": test.stage, "Request detail": test.debug, "Partial inventory": test.warning, "Completed": false} {
				if got := strings.Contains(diagnostics.String(), message); got != want {
					t.Errorf("%s present = %t, want %t: %s", message, got, want, diagnostics.String())
				}
			}
			decoder := json.NewDecoder(&diagnostics)
			for {
				var record map[string]any
				if err := decoder.Decode(&record); errors.Is(err, io.EOF) {
					break
				} else if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDaemonStagesStayAtDebug(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		command, output := newRootCommand()
		var diagnostics, report bytes.Buffer
		command.SetOut(&report)
		command.SetErr(&diagnostics)
		run, _, err := command.Find([]string{"run"})
		if err != nil {
			t.Fatal(err)
		}
		run.RunE = func(cmd *cobra.Command, _ []string) error {
			output.logger.InfoContext(cmd.Context(), "Fetching inventory", "stage", true)
			output.logger.Info("Device changed")
			return nil
		}
		args := []string{"run"}
		if verbose {
			args = append(args, "--verbose")
		}
		command.SetArgs(args)
		executed, err := command.ExecuteC()
		output.finish(executed, err)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(diagnostics.String(), "Fetching inventory") != verbose {
			t.Fatalf("daemon stages: %s", diagnostics.String())
		}
		if !strings.Contains(diagnostics.String(), "Device changed") || strings.Contains(diagnostics.String(), "Completed") || report.Len() != 0 {
			t.Fatalf("daemon output: %s / %s", diagnostics.String(), report.String())
		}
		decoder := json.NewDecoder(&diagnostics)
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
		}
	}
}

func TestInvalidOutputOptionsNeverExecute(t *testing.T) {
	for _, flags := range [][]string{{"--log-level", "trace"}, {"--log-format", "xml"}, {"--quiet", "--verbose"}} {
		command, output := newRootCommand()
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		called := false
		command.AddCommand(&cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { called = true; return nil }})
		command.SetArgs(append([]string{"probe"}, flags...))
		executed, err := command.ExecuteC()
		output.finish(executed, err)
		if err == nil || called {
			t.Fatalf("flags %v: executed=%t error=%v", flags, called, err)
		}
	}
}

func TestEarlyFailureProducesOneJSONReport(t *testing.T) {
	command, output := newRootCommand()
	var report, diagnostics bytes.Buffer
	command.SetOut(&report)
	command.SetErr(&diagnostics)
	command.SetArgs([]string{"plan", "--config", "missing.yaml", "--output", "json", "--log-format", "json"})
	executed, err := command.ExecuteC()
	output.finish(executed, err)
	if err == nil {
		t.Fatal("expected failure")
	}
	var result struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(report.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Fatalf("report = %s", report.String())
	}
	if !strings.Contains(diagnostics.String(), `"level":"ERROR"`) {
		t.Fatalf("diagnostics = %s", diagnostics.String())
	}
}

func TestExplicitVerbosityOverridesConfiguredLevel(t *testing.T) {
	for _, verbose := range []bool{false, true} {
		command, output := newRootCommand()
		t.Setenv(strings.ToUpper(strings.ReplaceAll(command.Name(), "-", "_"))+"_LOG_LEVEL", "warn")
		path := writeCommandConfig(t, t.TempDir(), "config.yaml", commandConfig)
		var diagnostics bytes.Buffer
		command.SetOut(io.Discard)
		command.SetErr(&diagnostics)
		command.AddCommand(&cobra.Command{Use: "probe", RunE: func(cmd *cobra.Command, _ []string) error {
			if _, err := loadConfig(cmd, []string{path}, output); err != nil {
				return err
			}
			output.logger.Debug("Request detail")
			return nil
		}})
		args := []string{"probe"}
		if verbose {
			args = append(args, "--verbose")
		}
		command.SetArgs(args)
		executed, err := command.ExecuteC()
		output.finish(executed, err)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(diagnostics.String(), "Request detail") != verbose {
			t.Fatalf("configured verbosity: %s", diagnostics.String())
		}
		if !verbose && diagnostics.Len() != 0 {
			t.Fatalf("warn level emitted routine diagnostics: %s", diagnostics.String())
		}
		if strings.ContainsAny(diagnostics.String(), "\x1b\r") {
			t.Fatalf("non-terminal output contains control sequences: %q", diagnostics.String())
		}
	}
}
