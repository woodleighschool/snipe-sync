package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
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
  skip:
    - when: device.serial_number == "FIELD-SKIP"
      fields: [name]
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
