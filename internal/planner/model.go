// Package planner produces deterministic user and asset reconciliation plans.
package planner

import (
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

// UserAction identifies one target user decision.
type UserAction string

const (
	UserCreate  UserAction = "create"
	UserUpdate  UserAction = "update"
	UserDisable UserAction = "disable"
	UserNoop    UserAction = "noop"
)

// AssetResult identifies whether an asset changes, remains unchanged, or is skipped.
type AssetResult string

const (
	AssetChange    AssetResult = "change"
	AssetUnchanged AssetResult = "unchanged"
	AssetSkipped   AssetResult = "skipped"
)

// UserPlan is one complete target user decision.
type UserPlan struct {
	Email    string           `json:"email"`
	Action   UserAction       `json:"action"`
	TargetID int64            `json:"target_id,omitempty"`
	Patch    domain.UserPatch `json:"patch"`
}

// AssetPlan is one complete target asset decision.
type AssetPlan struct {
	Source            string            `json:"source"`
	Namespace         string            `json:"namespace,omitempty"`
	DeviceID          string            `json:"device_id,omitempty"`
	SerialNumber      string            `json:"serial_number"`
	AssetID           int64             `json:"asset_id,omitempty"`
	CurrentName       string            `json:"current_name,omitempty"`
	DesiredName       string            `json:"desired_name,omitempty"`
	CurrentAssignment string            `json:"current_assignment,omitempty"`
	DesiredAssignment string            `json:"desired_assignment,omitempty"`
	CurrentManagedBy  string            `json:"current_managed_by,omitempty"`
	DesiredManagedBy  string            `json:"desired_managed_by,omitempty"`
	CurrentStatus     string            `json:"current_status,omitempty"`
	DesiredStatus     string            `json:"desired_status,omitempty"`
	Patch             domain.AssetPatch `json:"patch"`
	Checkin           bool              `json:"checkin"`
	CheckoutUser      string            `json:"checkout_user,omitempty"`
	CheckoutAt        time.Time         `json:"checkout_at,omitzero"`
	Result            AssetResult       `json:"result"`
	SkipReason        string            `json:"skip_reason,omitempty"`
	Note              string            `json:"note,omitempty"`
}

// HasChanges reports whether the plan contains any target mutation.
func (p AssetPlan) HasChanges() bool {
	return !p.Patch.Empty() || p.Checkin || p.CheckoutUser != ""
}

// Plan is one immutable complete reconciliation decision set.
type Plan struct {
	Warnings []string    `json:"warnings,omitempty"`
	Users    []UserPlan  `json:"users"`
	Assets   []AssetPlan `json:"assets"`
}

// UserCounts summarizes plan actions.
func (p Plan) UserCounts() map[UserAction]int {
	counts := map[UserAction]int{UserCreate: 0, UserUpdate: 0, UserDisable: 0, UserNoop: 0}
	for _, user := range p.Users {
		counts[user.Action]++
	}
	return counts
}

// AssetCounts summarizes asset results.
func (p Plan) AssetCounts() map[AssetResult]int {
	counts := map[AssetResult]int{AssetChange: 0, AssetUnchanged: 0, AssetSkipped: 0}
	for _, asset := range p.Assets {
		counts[asset.Result]++
	}
	return counts
}

// Metadata contains case-normalized target metadata indexes.
type Metadata struct {
	Departments   map[string][]int64
	Locations     map[string][]int64
	Statuses      map[string][]int64
	Manufacturers map[string]int
}

// Input contains complete authoritative source and target snapshots.
type Input struct {
	Users           []domain.User
	DevicesBySource map[string][]domain.Device
	TargetUsers     map[string]domain.TargetUser
	Assets          map[string]domain.Asset
	Warnings        []string
}
