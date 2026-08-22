package planner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func TestPlannerReconcilesUsersAndPreservesIncompleteLocation(t *testing.T) {
	engine := newPlanner(t, true)
	createdAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	input := plannerInput()
	input.Users = []domain.User{
		{Present: true, GivenName: "New", Surname: "User", MailNickname: "new", UserPrincipalName: "NEW@EXAMPLE.INVALID", Department: "Staff", CreatedAt: createdAt, Groups: []string{"staff"}, GroupsComplete: true},
		{Present: true, GivenName: "Casey", Surname: "Changed", MailNickname: "casey", UserPrincipalName: "casey@example.invalid", Department: "Staff", GroupsComplete: false},
		{Present: true, GivenName: "Same", Surname: "User", MailNickname: "same", UserPrincipalName: "same@example.invalid", Department: "Staff", Groups: []string{"staff"}, GroupsComplete: true},
	}
	input.TargetUsers = map[string]domain.TargetUser{
		"casey@example.invalid": {ID: 1, Email: "casey@example.invalid", GivenName: "Casey", Surname: "Old", Username: "casey", DepartmentID: 2, LocationID: 99},
		"same@example.invalid":  {ID: 2, Email: "same@example.invalid", GivenName: "Same", Surname: "User", Username: "same", DepartmentID: 2, LocationID: 3},
		"gone@example.invalid":  {ID: 3, Email: "gone@example.invalid", DepartmentID: 2},
		"done@example.invalid":  {ID: 4, Email: "done@example.invalid", DepartmentID: 17},
	}

	plan, err := engine.Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	users := userPlansByEmail(plan.Users)
	if users["new@example.invalid"].Action != planner.UserCreate {
		t.Errorf("new user action = %q, want create", users["new@example.invalid"].Action)
	}
	if got := users["new@example.invalid"].Patch.LocationID; got == nil || *got != 3 {
		t.Errorf("new user location = %v, want 3", got)
	}
	casey := users["casey@example.invalid"]
	if casey.Action != "update" || casey.Patch.Surname == nil || *casey.Patch.Surname != "Changed" {
		t.Errorf("Casey plan = %#v, want surname update", casey)
	}
	if casey.Patch.LocationID != nil {
		t.Error("incomplete group enrichment changed location")
	}
	if users["same@example.invalid"].Action != "noop" {
		t.Errorf("same user action = %q, want noop", users["same@example.invalid"].Action)
	}
	if users["gone@example.invalid"].Action != "disable" {
		t.Errorf("absent user action = %q, want disable", users["gone@example.invalid"].Action)
	}
	if _, exists := users["done@example.invalid"]; exists {
		t.Error("already-disabled absent user produced a plan")
	}
}

func TestPlannerSelectsNewestRecordAndHighestPrioritySource(t *testing.T) {
	engine := newPlanner(t, true)
	input := plannerInput()
	input.DevicesBySource = map[string][]domain.Device{
		"primary": {
			{ID: "old", SerialNumber: "serial-1", Name: "OLD", LastContactAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
			{ID: "new", SerialNumber: " serial-1 ", Name: "NEW (2)", LastContactAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)},
			{ID: "missing", SerialNumber: "missing"},
		},
		"secondary": {
			{ID: "secondary", SerialNumber: "SERIAL-1", Name: "LOWER-PRIORITY", LastContactAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	input.Assets = map[string]domain.Asset{
		"SERIAL-1": {ID: 1, SerialNumber: "SERIAL-1", Name: "CURRENT", Manufacturer: "Example Computers", Status: "Ready", StatusID: 2, ManagedBy: "Secondary"},
	}

	plan, err := engine.Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	assets := assetPlansBySerial(plan.Assets)
	selected := assets["SERIAL-1"]
	if selected.Source != "primary" || selected.DeviceID != "new" || selected.DesiredName != "NEW" {
		t.Errorf("selected plan = %#v, want newest primary record", selected)
	}
	if selected.Patch.ManagedBy == nil || *selected.Patch.ManagedBy != "Primary MDM" {
		t.Errorf("managed-by patch = %v, want Primary MDM", selected.Patch.ManagedBy)
	}
	if got := assets["MISSING"].SkipReason; got != "missing in Snipe" {
		t.Errorf("missing asset reason = %q", got)
	}
}

func TestPlannerPreservesAndChangesAssignmentsAtEvidenceBoundary(t *testing.T) {
	engine := newPlanner(t, false)
	input := plannerInput()
	input.Users = []domain.User{{Present: true, GivenName: "Created", MailNickname: "created", UserPrincipalName: "created@example.invalid", GroupsComplete: true}}
	input.TargetUsers = map[string]domain.TargetUser{
		"desired@example.invalid": {ID: 10, Email: "desired@example.invalid"},
		"current@example.invalid": {ID: 11, Email: "current@example.invalid"},
	}
	input.DevicesBySource = map[string][]domain.Device{
		"primary": {
			{ID: "reassign", SerialNumber: "REASSIGN", Name: "DEVICE", PrimaryUserPrincipalName: "desired@example.invalid"},
			{ID: "created", SerialNumber: "CREATED", Name: "DEVICE", PrimaryUserPrincipalName: "created@example.invalid"},
			{ID: "missing", SerialNumber: "MISSING-USER", Name: "DEVICE", PrimaryUserPrincipalName: "missing@example.invalid"},
			{ID: "blank", SerialNumber: "BLANK", Name: ""},
			{ID: "shared", SerialNumber: "SHARED", Name: "SHARED-CART"},
			{ID: "checkin", SerialNumber: "CHECKIN", Name: "ORDINARY"},
			{ID: "correct", SerialNumber: "CORRECT", Name: "DEVICE", PrimaryUserPrincipalName: "desired@example.invalid"},
		},
		"secondary": {},
	}
	input.Assets = map[string]domain.Asset{}
	for index, serial := range []string{"REASSIGN", "CREATED", "MISSING-USER", "BLANK", "SHARED", "CHECKIN", "CORRECT"} {
		input.Assets[serial] = domain.Asset{
			ID: int64(index + 1), SerialNumber: serial, Name: input.DevicesBySource["primary"][index].Name,
			Manufacturer: "Example Computers", Status: "Ready", StatusID: 2,
			AssignedToID: 11, AssignedToType: "user", AssignedEmail: "current@example.invalid", ManagedBy: "Primary MDM",
		}
	}
	correct := input.Assets["CORRECT"]
	correct.AssignedToID = 10
	correct.AssignedEmail = "desired@example.invalid"
	input.Assets["CORRECT"] = correct

	plan, err := engine.Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	assets := assetPlansBySerial(plan.Assets)
	if !assets["REASSIGN"].Checkin || assets["REASSIGN"].CheckoutUser != "desired@example.invalid" {
		t.Errorf("reassignment = %#v", assets["REASSIGN"])
	}
	if assets["CREATED"].CheckoutUser != "created@example.invalid" || !strings.Contains(assets["CREATED"].Note, "planned user create") {
		t.Errorf("planned-user checkout = %#v", assets["CREATED"])
	}
	if assets["MISSING-USER"].Checkin || assets["MISSING-USER"].CheckoutUser != "" || !strings.Contains(assets["MISSING-USER"].Note, "missing in Snipe") {
		t.Errorf("missing-user preservation = %#v", assets["MISSING-USER"])
	}
	if assets["BLANK"].Checkin || !strings.Contains(assets["BLANK"].Note, "blank MDM") {
		t.Errorf("blank-name preservation = %#v", assets["BLANK"])
	}
	if assets["SHARED"].Checkin || !strings.Contains(assets["SHARED"].Note, "shared-device") {
		t.Errorf("shared-device preservation = %#v", assets["SHARED"])
	}
	if !assets["CHECKIN"].Checkin || assets["CHECKIN"].CheckoutUser != "" {
		t.Errorf("ordinary checkin = %#v", assets["CHECKIN"])
	}
	if assets["CORRECT"].Checkin || assets["CORRECT"].CheckoutUser != "" {
		t.Errorf("correct assignment = %#v", assets["CORRECT"])
	}
}

func TestPlannerPromotesSkipsAndReconcilesAbsentAssets(t *testing.T) {
	engine := newPlanner(t, true)
	input := plannerInput()
	input.DevicesBySource = map[string][]domain.Device{
		"primary": {
			{ID: "stock", SerialNumber: "STOCK", Name: "DEVICE"},
			{ID: "blocked", SerialNumber: "BLOCKED", Name: "DEVICE"},
			{ID: "archived", SerialNumber: "ARCHIVED", Name: "DEVICE"},
			{ID: "unsupported", SerialNumber: "UNSUPPORTED", Name: "DEVICE"},
		},
		"secondary": {},
	}
	input.Assets = map[string]domain.Asset{
		"STOCK":       {ID: 1, SerialNumber: "STOCK", Manufacturer: "Example Computers", Status: "Stock", StatusID: 6},
		"BLOCKED":     {ID: 2, SerialNumber: "BLOCKED", Manufacturer: "Example Computers", Status: "Repair", StatusID: 7},
		"ARCHIVED":    {ID: 3, SerialNumber: "ARCHIVED", Manufacturer: "Example Computers", Status: "Archived", StatusID: 3, Archived: true},
		"UNSUPPORTED": {ID: 4, SerialNumber: "UNSUPPORTED", Manufacturer: "Other Vendor", Status: "Ready", StatusID: 2},
		"ABSENT":      {ID: 5, SerialNumber: "ABSENT", Name: "OLD", Manufacturer: "Example Computers", Status: "Repair", StatusID: 7, ManagedBy: "Primary MDM", AssignedToID: 11, AssignedToType: "user"},
	}

	plan, err := engine.Plan(input)
	if err != nil {
		t.Fatal(err)
	}
	assets := assetPlansBySerial(plan.Assets)
	if got := assets["STOCK"].Patch.StatusID; got == nil || *got != 2 {
		t.Errorf("stock promotion = %v, want status 2", got)
	}
	if got := assets["BLOCKED"].SkipReason; got != "Repair blocks sync" {
		t.Errorf("blocked reason = %q", got)
	}
	if assets["ARCHIVED"].SkipReason != "archived" || !strings.Contains(assets["UNSUPPORTED"].SkipReason, "unsupported") {
		t.Errorf("skip plans = %#v %#v", assets["ARCHIVED"], assets["UNSUPPORTED"])
	}
	absent := assets["ABSENT"]
	if absent.Source != "absent" || absent.Patch.Name == nil || *absent.Patch.Name != "" || absent.Patch.ManagedBy == nil || !absent.Checkin {
		t.Errorf("absent plan = %#v", absent)
	}
}

func TestPlannerFailsClosedOnIncompleteConfiguredSource(t *testing.T) {
	engine := newPlanner(t, true)
	input := plannerInput()
	delete(input.DevicesBySource, "secondary")
	_, err := engine.Plan(input)
	if err == nil || !strings.Contains(err.Error(), `source "secondary" snapshot is incomplete`) {
		t.Fatalf("Plan error = %v, want incomplete-source error", err)
	}
}

func newPlanner(t *testing.T, absent bool) *planner.Planner {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	absentText := "false"
	if absent {
		absentText = "true"
	}
	contents := strings.Replace(plannerConfig, "ABSENT_ENABLED", absentText, 1)
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := planner.New(cfg, planner.Metadata{
		Departments:   map[string][]int64{"staff": {2}, "disabled": {17}},
		Locations:     map[string][]int64{"main campus": {3}},
		Statuses:      map[string][]int64{"ready": {2}, "stock": {6}, "archived": {3}, "repair": {7}},
		Manufacturers: map[string]int{"example computers": 1, "other vendor": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func plannerInput() planner.Input {
	return planner.Input{
		DevicesBySource: map[string][]domain.Device{"primary": {}, "secondary": {}},
		TargetUsers:     map[string]domain.TargetUser{},
		Assets:          map[string]domain.Asset{},
	}
}

func userPlansByEmail(plans []planner.UserPlan) map[string]planner.UserPlan {
	result := make(map[string]planner.UserPlan, len(plans))
	for _, plan := range plans {
		result[plan.Email] = plan
	}
	return result
}

func assetPlansBySerial(plans []planner.AssetPlan) map[string]planner.AssetPlan {
	result := make(map[string]planner.AssetPlan, len(plans))
	for _, plan := range plans {
		result[plan.SerialNumber] = plan
	}
	return result
}

const plannerConfig = `
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
    managed_by: Primary MDM
  - name: secondary
    type: jamf
    connection: mdm
    priority: 50
    managed_by: Secondary MDM
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
    enabled: ABSENT_ENABLED
`
