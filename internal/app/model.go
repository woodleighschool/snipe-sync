// Package app composes providers around the deterministic reconciliation planner.
package app

import (
	"context"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

// DeviceSource provides one complete named managed-device snapshot.
type DeviceSource interface {
	Name() string
	ListDevices(context.Context) ([]domain.Device, error)
}

// IdentitySource provides one complete directory snapshot plus non-fatal enrichment warnings.
type IdentitySource interface {
	ListUsers(context.Context) ([]domain.User, []string, error)
}

// TargetSnapshot is the complete target state and metadata needed by the planner.
type TargetSnapshot struct {
	Users           map[string]domain.TargetUser
	Assets          map[string]domain.Asset
	Departments     map[string][]int64
	Locations       map[string][]int64
	Statuses        map[string][]int64
	Manufacturers   map[string]int
	ManagedByColumn string
}

// Target provides the Snipe snapshot and ordered mutation primitives.
type Target interface {
	Snapshot(context.Context, string) (*TargetSnapshot, error)
	CreateUser(context.Context, domain.UserPatch) (int64, error)
	PatchUser(context.Context, int64, domain.UserPatch) error
	PatchAsset(context.Context, int64, domain.AssetPatch, string) error
	CheckinAsset(context.Context, int64) error
	CheckoutAsset(context.Context, int64, int64, time.Time, *time.Location) error
}

// Failure identifies one independent mutation that could not be completed.
type Failure struct {
	Kind       string `json:"kind"`
	Identifier string `json:"identifier"`
	Error      string `json:"error"`
}

// ApplyResult summarizes writes attempted from one immutable plan.
type ApplyResult struct {
	UsersApplied  int       `json:"users_applied"`
	AssetsApplied int       `json:"assets_applied"`
	Failures      []Failure `json:"failures,omitempty"`
}

// Result combines a complete plan with the outcome of applying it.
type Result struct {
	Plan  planner.Plan `json:"plan"`
	Apply *ApplyResult `json:"apply,omitempty"`
}
