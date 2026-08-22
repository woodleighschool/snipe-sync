package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
    staff: [group-1]
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
      - when: '"staff" in user.groups'
        value: Main Campus
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
  assignment:
    shared_when: device.name.startsWith("SHARED-")
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
	cfg, err := load([]string{base, overlay}, func(string) (string, bool) { return "", false })
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

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, "config.yaml", validConfig+"unexpected: true\n")
	_, err := load([]string{path}, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Load error = %v, want unknown-field error", err)
	}
}

func TestLoadRequiresWholeValueEnvironmentPlaceholders(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(validConfig, "tenant_id: tenant", "tenant_id: prefix-${TENANT_ID}", 1))
	_, err := load([]string{path}, func(string) (string, bool) { return "value", true })
	if err == nil || !strings.Contains(err.Error(), "must occupy an entire YAML scalar") {
		t.Fatalf("Load error = %v, want whole-scalar error", err)
	}
}

func TestLoadCompilesTypedPolicy(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(validConfig, `include_when: user.given_name != ""`, "include_when: user.given_name", 1))
	_, err := load([]string{path}, func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "want bool") {
		t.Fatalf("Load error = %v, want CEL type error", err)
	}
}

func TestLoadRejectsDuplicateSourcePriority(t *testing.T) {
	path := writeConfig(t, "config.yaml", strings.Replace(validConfig, "priority: 50", "priority: 100", 1))
	_, err := load([]string{path}, func(string) (string, bool) { return "", false })
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
