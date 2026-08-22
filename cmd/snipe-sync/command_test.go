package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/app"
)

func TestDefaultConfigPathsUsesConfigInCurrentDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Chdir(directory)
	if got := defaultConfigPaths(); got != nil {
		t.Fatalf("defaultConfigPaths without config = %v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(directory, "config.yaml"), []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := defaultConfigPaths(), []string{"config.yaml"}; !slices.Equal(got, want) {
		t.Fatalf("defaultConfigPaths = %v, want %v", got, want)
	}
}

func TestValidateLoadsOrderedConfigurationFiles(t *testing.T) {
	directory := t.TempDir()
	basePath := writeCommandConfig(t, directory, "base.yaml", commandConfig)
	overlayPath := writeCommandConfig(t, directory, "overlay.yaml", `assets:
  assignment:
    shared_when: device.name.startsWith("SHARED-")
`)
	command := newRootCommand()
	command.SetArgs([]string{"validate", "--config", basePath, "--config", overlayPath})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if got, want := output.String(), "configuration valid\n"; got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
}

func TestRunLoopCancelsInFlightReconciliation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	service := blockingReconciler{started: started}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runLoop(ctx, time.Hour, service, slog.New(slog.DiscardHandler))
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runLoop did not stop after cancellation")
	}
}

func TestRunCycleKeepsRoutineDecisionsAtDebug(t *testing.T) {
	result := app.Result{Plan: outputFixture(), Apply: &app.ApplyResult{UsersApplied: 1, AssetsApplied: 1}}
	service := staticReconciler{result: result}

	var infoOutput bytes.Buffer
	infoLogger := slog.New(slog.NewJSONHandler(&infoOutput, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := runCycle(context.Background(), service, infoLogger); err != nil {
		t.Fatal(err)
	}
	infoRecords := decodeLogRecords(t, infoOutput.Bytes())
	for _, record := range infoRecords {
		if record.Message == "asset evaluated" || record.Serial == "SERIAL-2" {
			t.Errorf("info logs contain routine asset: %#v", record)
		}
	}

	var debugOutput bytes.Buffer
	debugLogger := slog.New(slog.NewJSONHandler(&debugOutput, &slog.HandlerOptions{Level: slog.LevelDebug}))
	if err := runCycle(context.Background(), service, debugLogger); err != nil {
		t.Fatal(err)
	}
	debugRecords := decodeLogRecords(t, debugOutput.Bytes())
	foundRoutine := false
	for _, record := range debugRecords {
		if record.Message == "asset evaluated" && record.Serial == "SERIAL-2" && record.Level == "DEBUG" {
			foundRoutine = true
		}
	}
	if !foundRoutine {
		t.Errorf("debug logs do not contain routine asset: %s", debugOutput.String())
	}
}

func TestRunCycleDoesNotReportUnknownOutcomesAsApplied(t *testing.T) {
	result := app.Result{Plan: outputFixture(), Apply: &app.ApplyResult{}}
	service := staticReconciler{result: result, err: context.Canceled}
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if err := runCycle(context.Background(), service, logger); !errors.Is(err, context.Canceled) {
		t.Fatalf("runCycle error = %v, want context cancellation", err)
	}
	for _, record := range decodeLogRecords(t, output.Bytes()) {
		if record.Message == "user reconciled" || record.Message == "asset reconciled" {
			t.Errorf("interrupted cycle reported unknown outcome as applied: %#v", record)
		}
	}
}

type blockingReconciler struct {
	started chan<- struct{}
}

func (r blockingReconciler) Reconcile(ctx context.Context, _ bool) (app.Result, error) {
	close(r.started)
	<-ctx.Done()
	return app.Result{}, ctx.Err()
}

type staticReconciler struct {
	result app.Result
	err    error
}

func (r staticReconciler) Reconcile(context.Context, bool) (app.Result, error) {
	return r.result, r.err
}

type logRecord struct {
	Level   string `json:"level"`
	Message string `json:"msg"`
	Serial  string `json:"serial"`
}

func decodeLogRecords(t *testing.T, data []byte) []logRecord {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var records []logRecord
	for {
		var record logRecord
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				return records
			}
			t.Fatalf("decode log: %v", err)
		}
		records = append(records, record)
	}
}

func writeCommandConfig(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const commandConfig = `
version: 1
connections:
  microsoft:
    type: microsoft_graph
    tenant_id: tenant
    client_id: client
    client_secret: secret
  snipe:
    type: snipeit
    url: https://assets.example.invalid/api/v1
    api_key: token
identity:
  name: entra
  type: entra
  connection: microsoft
  domains: [example.invalid]
devices:
  - name: primary
    type: intune
    connection: microsoft
    priority: 100
target:
  type: snipeit
  connection: snipe
  timezone: Australia/Melbourne
users:
  absent:
    department: Disabled
assets:
  manufacturers: [Example Computers]
  statuses:
    writable: [Ready]
  managed_by_field: Managed By
`
