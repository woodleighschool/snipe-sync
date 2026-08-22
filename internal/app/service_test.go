package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func TestPlanIsReadOnly(t *testing.T) {
	target := fixtureTarget()
	target.snapshot.Assets["SERIAL-1"] = domain.Asset{
		ID: 1, SerialNumber: "SERIAL-1", Name: "OLD", Manufacturer: "Example Computers",
		Status: "Ready", StatusID: 2, ManagedBy: "Primary MDM",
	}
	service := newTestService(t, &fakeIdentity{}, []DeviceSource{&fakeSource{
		name: "primary", devices: []domain.Device{{ID: "device-1", SerialNumber: "SERIAL-1", Name: "NEW"}},
	}}, target)

	result, err := service.Reconcile(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Plan.Assets[0].Result; got != planner.AssetChange {
		t.Fatalf("asset result = %q, want change", got)
	}
	if result.Apply != nil {
		t.Errorf("apply result = %#v, want nil", result.Apply)
	}
	if len(target.calls) != 0 {
		t.Errorf("target writes = %#v, want none", target.calls)
	}
}

func TestApplyCreatesUsersBeforeDependentCheckout(t *testing.T) {
	target := fixtureTarget()
	target.snapshot.Assets["SERIAL-1"] = domain.Asset{
		ID: 7, SerialNumber: "SERIAL-1", Name: "DEVICE", Manufacturer: "Example Computers",
		Status: "Ready", StatusID: 2, ManagedBy: "Primary MDM",
	}
	target.snapshot.Users["a@example.invalid"] = domain.TargetUser{
		ID: 9, Email: "a@example.invalid", GivenName: "Old", Username: "a",
	}
	identity := &fakeIdentity{users: []domain.User{
		{
			Present: true, GivenName: "Person", MailNickname: "person",
			UserPrincipalName: "person@example.invalid", GroupsComplete: true,
		},
		{
			Present: true, GivenName: "New", MailNickname: "a",
			UserPrincipalName: "a@example.invalid", GroupsComplete: true,
		},
	}}
	service := newTestService(t, identity, []DeviceSource{&fakeSource{
		name: "primary", devices: []domain.Device{{
			ID: "device-1", SerialNumber: "SERIAL-1", Name: "DEVICE",
			PrimaryUserPrincipalName: "person@example.invalid",
		}},
	}}, target)

	result, err := service.Reconcile(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Apply == nil || result.Apply.UsersApplied != 2 || result.Apply.AssetsApplied != 1 {
		t.Fatalf("apply result = %#v", result.Apply)
	}
	want := []string{"create-user", "patch-user:9", "checkout-asset:7:user:42"}
	if !reflect.DeepEqual(target.calls, want) {
		t.Errorf("target calls = %#v, want %#v", target.calls, want)
	}
}

func TestApplyUsesGlobalAssetPhases(t *testing.T) {
	target := fixtureTarget()
	target.snapshot.Users["one@example.invalid"] = domain.TargetUser{ID: 11, Email: "one@example.invalid"}
	target.snapshot.Users["two@example.invalid"] = domain.TargetUser{ID: 12, Email: "two@example.invalid"}
	service := newTestService(t, &fakeIdentity{}, []DeviceSource{&fakeSource{name: "primary"}}, target)
	name := "First"
	managedBy := "Primary MDM"
	plan := planner.Plan{Assets: []planner.AssetPlan{
		{
			Result: planner.AssetChange, AssetID: 1, SerialNumber: "SERIAL-1",
			Patch: domain.AssetPatch{Name: &name}, Checkin: true, CheckoutUser: "one@example.invalid",
		},
		{
			Result: planner.AssetChange, AssetID: 2, SerialNumber: "SERIAL-2",
			Patch: domain.AssetPatch{ManagedBy: &managedBy}, Checkin: true, CheckoutUser: "two@example.invalid",
		},
	}}

	result, err := service.apply(context.Background(), plan, target.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsApplied != 2 {
		t.Fatalf("AssetsApplied = %d, want 2", result.AssetsApplied)
	}
	want := []string{
		"patch-asset:1", "patch-asset:2",
		"checkin-asset:1", "checkin-asset:2",
		"checkout-asset:1:user:11", "checkout-asset:2:user:12",
	}
	if !reflect.DeepEqual(target.calls, want) {
		t.Errorf("target calls = %#v, want %#v", target.calls, want)
	}
}

func TestApplySkipsAssignmentWhenCreatedUserIsUnavailable(t *testing.T) {
	target := fixtureTarget()
	target.createErr = errors.New("create failed")
	service := newTestService(t, &fakeIdentity{}, []DeviceSource{&fakeSource{name: "primary"}}, target)
	name := "Renamed"
	plan := planner.Plan{
		Users: []planner.UserPlan{{
			Action: planner.UserCreate, Email: "new@example.invalid",
			Patch: domain.UserPatch{Email: new("new@example.invalid")},
		}},
		Assets: []planner.AssetPlan{
			{
				Result: planner.AssetChange, AssetID: 1, SerialNumber: "SERIAL-1",
				Patch: domain.AssetPatch{Name: &name}, Checkin: true, CheckoutUser: "new@example.invalid",
			},
			{Result: planner.AssetChange, AssetID: 2, SerialNumber: "SERIAL-2", Checkin: true},
		},
	}

	result, err := service.apply(context.Background(), plan, target.snapshot)
	if err == nil {
		t.Fatal("apply error = nil, want failure")
	}
	if result.AssetsApplied != 1 || len(result.Failures) != 2 {
		t.Fatalf("apply result = %#v", result)
	}
	want := []string{"create-user", "patch-asset:1", "checkin-asset:2"}
	if !reflect.DeepEqual(target.calls, want) {
		t.Errorf("target calls = %#v, want %#v", target.calls, want)
	}
}

func TestIncompleteSourcePreventsAllWrites(t *testing.T) {
	target := fixtureTarget()
	service := newTestService(t, &fakeIdentity{}, []DeviceSource{&fakeSource{
		name: "primary", err: errors.New("inventory unavailable"),
	}}, target)

	_, err := service.Reconcile(context.Background(), true)
	if err == nil {
		t.Fatal("Reconcile error = nil, want source failure")
	}
	if len(target.calls) != 0 {
		t.Errorf("target writes = %#v, want none", target.calls)
	}
}

type fakeIdentity struct {
	users    []domain.User
	warnings []string
	err      error
}

func (f *fakeIdentity) ListUsers(context.Context) ([]domain.User, []string, error) {
	return f.users, f.warnings, f.err
}

type fakeSource struct {
	name    string
	devices []domain.Device
	err     error
}

func (f *fakeSource) Name() string {
	return f.name
}

func (f *fakeSource) ListDevices(context.Context) ([]domain.Device, error) {
	return f.devices, f.err
}

type fakeTarget struct {
	snapshot  *TargetSnapshot
	calls     []string
	createErr error
}

func fixtureTarget() *fakeTarget {
	return &fakeTarget{snapshot: &TargetSnapshot{
		Users: map[string]domain.TargetUser{}, Assets: map[string]domain.Asset{},
		Departments: map[string][]int64{"disabled": {17}}, Locations: map[string][]int64{},
		Statuses: map[string][]int64{"ready": {2}}, Manufacturers: map[string]int{"example computers": 1},
		ManagedByColumn: "_snipeit_managed_by_1",
	}}
}

func (f *fakeTarget) Snapshot(context.Context, string) (*TargetSnapshot, error) {
	return f.snapshot, nil
}

func (f *fakeTarget) CreateUser(context.Context, domain.UserPatch) (int64, error) {
	f.calls = append(f.calls, "create-user")
	return 42, f.createErr
}

func (f *fakeTarget) PatchUser(_ context.Context, userID int64, _ domain.UserPatch) error {
	f.calls = append(f.calls, "patch-user:"+strconv.FormatInt(userID, 10))
	return nil
}

func (f *fakeTarget) PatchAsset(_ context.Context, assetID int64, _ domain.AssetPatch, _ string) error {
	f.calls = append(f.calls, "patch-asset:"+strconv.FormatInt(assetID, 10))
	return nil
}

func (f *fakeTarget) CheckinAsset(_ context.Context, assetID int64) error {
	f.calls = append(f.calls, "checkin-asset:"+strconv.FormatInt(assetID, 10))
	return nil
}

func (f *fakeTarget) CheckoutAsset(_ context.Context, assetID, userID int64, _ time.Time, _ *time.Location) error {
	f.calls = append(f.calls, "checkout-asset:"+strconv.FormatInt(assetID, 10)+":user:"+strconv.FormatInt(userID, 10))
	return nil
}

func newTestService(t *testing.T, identity IdentitySource, sources []DeviceSource, target Target) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(testConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(cfg, identity, sources, target)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

const testConfig = `
version: 1
connections:
  microsoft:
    type: microsoft_graph
    tenant_id: tenant
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
devices:
  - name: primary
    type: intune
    connection: microsoft
    priority: 100
    managed_by: Primary MDM
target:
  type: snipeit
  connection: assets
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
