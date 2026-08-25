package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validConfig = `
version: 1
connections:
  microsoft:
    type: microsoft_graph
    tenant_id: tenant
    client_id: client
    client_secret: secret
  mdm:
    type: jamf
    url: https://mdm.example.invalid
    client_id: client
    client_secret: secret
  assets:
    type: snipeit
    url: https://assets.example.invalid/api/v1
    api_key: token
identity:
  name: directory
  type: entra
  connection: microsoft
  domains: [example.invalid]
  groups:
    group_a: [group-1]
devices:
  - name: primary
    type: intune
    connection: microsoft
    priority: 100
  - name: secondary
    type: jamf
    connection: mdm
    priority: 50
target:
  type: snipeit
  connection: assets
  timezone: Australia/Melbourne
users:
  include_when: user.given_name != ""
  location:
    cases:
      - when: '"group_a" in user.groups'
        value: Location A
  absent:
    department: Disabled
assets:
  manufacturers: [Example Computers]
  statuses:
    writable: [Ready]
    promote:
      from: [Stock]
      to: Ready
  managed_by_field: Managed By
  skip:
    - when: device.serial_number == "FIELD-SKIP"
      fields: [assignment]
  absent:
    enabled: true
`

func TestLoadMergesMappingsAndReplacesLists(t *testing.T) {
	base := writeConfig(t, "base.yaml", validConfig)
	overlay := writeConfig(t, "overlay.yaml", `
identity:
  domains: [other.invalid]
assets:
  absent:
    enabled: false
`)
	cfg, err := load([]string{base, overlay}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Identity.Domains, []string{"other.invalid"}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("domains = %v, want %v", got, want)
	}
	if cfg.Assets.Absent.Enabled {
		t.Fatal("overlay did not replace absent.enabled")
	}
	if cfg.Assets.ManagedByField != "Managed By" {
		t.Fatal("overlay discarded sibling mapping values")
	}
}

func TestLoadResolvesEnvironmentOverYAMLOverDefaults(t *testing.T) {
	local := strings.Replace(
		validConfig,
		"version: 1",
		"version: 1\nlog_level: warn\nreconcile:\n  poll_interval: 30s",
		1,
	)
	path := writeConfig(t, "local.yaml", local)
	cfg, err := load([]string{path}, nil)
	if err != nil {
		t.Fatalf("load() local config error = %v", err)
	}
	if got, want := cfg.LogLevel, "warn"; got != want {
		t.Errorf("local log level = %q, want %q", got, want)
	}
	if got, want := cfg.Reconcile.PollInterval.Duration, 30*time.Second; got != want {
		t.Errorf("local poll interval = %v, want %v", got, want)
	}

	cfg, err = load([]string{path}, map[string]string{
		"SNIPE_SYNC_LOG_LEVEL":               " DEBUG ",
		"SNIPE_SYNC_RECONCILE_POLL_INTERVAL": "45s",
	})
	if err != nil {
		t.Fatalf("load() environment override error = %v", err)
	}
	if got, want := cfg.LogLevel, "debug"; got != want {
		t.Errorf("environment log level = %q, want %q", got, want)
	}
	if got, want := cfg.Reconcile.PollInterval.Duration, 45*time.Second; got != want {
		t.Errorf("environment poll interval = %v, want %v", got, want)
	}
}

func TestLoadAppliesAndValidatesPollInterval(t *testing.T) {
	path := writeConfig(t, "config.yaml", validConfig)
	cfg, err := load([]string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Reconcile.PollInterval.Duration, time.Minute; got != want {
		t.Errorf("poll interval = %s, want %s", got, want)
	}
	custom := strings.Replace(validConfig, "version: 1", "version: 1\nreconcile:\n  poll_interval: 30s", 1)
	path = writeConfig(t, "custom.yaml", custom)
	cfg, err = load([]string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Reconcile.PollInterval.Duration, 30*time.Second; got != want {
		t.Errorf("custom poll interval = %s, want %s", got, want)
	}

	path = writeConfig(t, "invalid.yaml", "reconcile:\n  poll_interval: 0s\n"+validConfig)
	_, err = load([]string{path}, nil)
	if err == nil || !strings.Contains(err.Error(), "reconcile.poll_interval must be greater than zero") {
		t.Fatalf("Load error = %v, want poll interval error", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, "config.yaml", validConfig+"unexpected: true\n")
	_, err := load([]string{path}, nil)
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Load error = %v, want unknown-field error", err)
	}
}

func TestLoadRequiresWholeValueEnvironmentPlaceholders(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(validConfig, "tenant_id: tenant", "tenant_id: prefix-${TENANT_ID}", 1))
	_, err := load([]string{path}, map[string]string{"TENANT_ID": "value"})
	if err == nil || !strings.Contains(err.Error(), "must occupy an entire YAML scalar") {
		t.Fatalf("Load error = %v, want whole-scalar error", err)
	}
}

func TestLoadCompilesTypedPolicy(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(validConfig, `include_when: user.given_name != ""`, "include_when: user.given_name", 1))
	_, err := load([]string{path}, nil)
	if err == nil || !strings.Contains(err.Error(), "want bool") {
		t.Fatalf("Load error = %v, want CEL type error", err)
	}
}

func TestLoadCompilesTypedAssetFieldSkipPolicy(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(
		validConfig,
		`device.serial_number == "FIELD-SKIP"`,
		"device.name",
		1,
	))
	_, err := load([]string{path}, nil)
	if err == nil || !strings.Contains(err.Error(), "assets.skip[0].when") || !strings.Contains(err.Error(), "want bool") {
		t.Fatalf("Load error = %v, want asset skip CEL type error", err)
	}
}

func TestLoadRejectsInvalidAssetSkipFields(t *testing.T) {
	tests := map[string]struct {
		fields string
		error  string
	}{
		"empty":     {fields: "[]", error: "assets.skip[0].fields must not be empty"},
		"duplicate": {fields: "[name, name]", error: `assets.skip[0].fields contains duplicate "name"`},
		"unknown":   {fields: "[serial_number]", error: `assets.skip[0].fields[0]: unsupported field "serial_number"`},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "config.yaml", strings.Replace(validConfig, "[assignment]", test.fields, 1))
			_, err := load([]string{path}, nil)
			if err == nil || !strings.Contains(err.Error(), test.error) {
				t.Fatalf("Load error = %v, want %q", err, test.error)
			}
		})
	}
}

func TestLoadRejectsDuplicateSourcePriority(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(validConfig, "priority: 50", "priority: 100", 1))
	_, err := load([]string{path}, nil)
	if err == nil || !strings.Contains(err.Error(), "priority 100 is duplicated") {
		t.Fatalf("Load error = %v, want duplicate priority error", err)
	}
}

func writeConfig(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
