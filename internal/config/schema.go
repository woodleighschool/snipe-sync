// Package config loads and validates the versioned YAML policy.
package config

import (
	"time"

	"github.com/woodleighschool/snipe-sync/internal/expression"
)

// Config is the complete versioned configuration.
type Config struct {
	Version     int                   `yaml:"version"     jsonschema:"enum=1"`
	Connections map[string]Connection `yaml:"connections" jsonschema:"minProperties=1"`
	Identity    Identity              `yaml:"identity"`
	Devices     []DeviceSource        `yaml:"devices"     jsonschema:"minItems=1"`
	Target      Target                `yaml:"target"`
	Reconcile   Reconcile             `yaml:"reconcile,omitempty"`
	Users       UserPolicy            `yaml:"users"`
	Assets      AssetPolicy           `yaml:"assets"`
	Programs    Programs              `yaml:"-"`
}

// Reconcile controls the interval between completed reconciliation cycles.
type Reconcile struct {
	PollInterval Duration `yaml:"poll_interval,omitempty"`
}

// Connection contains credentials for one remote API.
type Connection struct {
	Type         string `yaml:"type"                    jsonschema:"enum=microsoft_graph,enum=jamf,enum=snipeit"`
	TenantID     string `yaml:"tenant_id,omitempty"`
	ClientID     string `yaml:"client_id,omitempty"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	URL          string `yaml:"url,omitempty"`
	APIKey       string `yaml:"api_key,omitempty"`
	BaseURL      string `yaml:"base_url,omitempty"`
}

// Identity selects the Entra directory and group aliases used by policy.
type Identity struct {
	Name       string              `yaml:"name"`
	Type       string              `yaml:"type"       jsonschema:"enum=entra"`
	Connection string              `yaml:"connection"`
	Domains    []string            `yaml:"domains"    jsonschema:"minItems=1,uniqueItems=true"`
	Groups     map[string][]string `yaml:"groups,omitempty"`
}

// DeviceSource selects one authoritative managed-device inventory.
type DeviceSource struct {
	Name       string `yaml:"name"`
	Type       string `yaml:"type"       jsonschema:"enum=intune,enum=jamf"`
	Connection string `yaml:"connection"`
	Priority   int    `yaml:"priority"`
	ManagedBy  string `yaml:"managed_by,omitempty"`
}

// Target selects the Snipe-IT instance and its timezone.
type Target struct {
	Type       string `yaml:"type"       jsonschema:"enum=snipeit"`
	Connection string `yaml:"connection"`
	Timezone   string `yaml:"timezone"`
}

// UserPolicy controls Entra selection and Snipe user lifecycle policy.
type UserPolicy struct {
	IncludeWhen string         `yaml:"include_when,omitempty"`
	Location    LocationPolicy `yaml:"location,omitempty"`
	Absent      UserAbsent     `yaml:"absent"`
}

// LocationPolicy contains first-match location rules.
type LocationPolicy struct {
	Cases []LocationCase `yaml:"cases,omitempty"`
}

// LocationCase maps a typed CEL condition to a Snipe location name.
type LocationCase struct {
	When  string `yaml:"when"`
	Value string `yaml:"value"`
}

// UserAbsent defines how internal target users absent from Entra are disabled.
type UserAbsent struct {
	Department string `yaml:"department"`
}

// AssetPolicy controls target eligibility, status transitions, assignment, and absence handling.
type AssetPolicy struct {
	Manufacturers  []string         `yaml:"manufacturers"    jsonschema:"minItems=1,uniqueItems=true"`
	Statuses       AssetStatuses    `yaml:"statuses"`
	ManagedByField string           `yaml:"managed_by_field"`
	Assignment     AssignmentPolicy `yaml:"assignment,omitempty"`
	Absent         AssetAbsent      `yaml:"absent,omitempty"`
}

// AssetStatuses identifies writable states and one optional promotion.
type AssetStatuses struct {
	Writable []string        `yaml:"writable" jsonschema:"minItems=1,uniqueItems=true"`
	Promote  StatusPromotion `yaml:"promote,omitempty"`
}

// StatusPromotion maps blocked source statuses to a writable target status.
type StatusPromotion struct {
	From []string `yaml:"from,omitempty" jsonschema:"uniqueItems=true"`
	To   string   `yaml:"to,omitempty"`
}

// AssignmentPolicy contains the shared-device preservation condition.
type AssignmentPolicy struct {
	SharedWhen string `yaml:"shared_when,omitempty"`
}

// AssetAbsent enables destructive lifecycle actions for assets absent from every configured source.
type AssetAbsent struct {
	Enabled bool `yaml:"enabled,omitempty"`
}

// Programs holds CEL programs compiled while loading configuration.
type Programs struct {
	UserInclude expression.Program
	Locations   []expression.Program
	SharedAsset expression.Program
}

// Duration is a YAML duration parsed with time.ParseDuration.
type Duration struct {
	time.Duration

	set bool
}
