package snipe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

func TestSnapshotReadsCompleteNormalizedTargetState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if got, want := request.Header.Get("Authorization"), "Bearer fixture-key"; got != want {
			t.Errorf("authorization = %q, want %q", got, want)
		}
		var rows []any
		switch request.URL.Path {
		case "/api/v1/users":
			rows = []any{map[string]any{
				"id": 7, "username": "casey", "email": "CASEY@EXAMPLE.INVALID",
				"first_name": "Casey", "last_name": "Unit", "start_date": map[string]string{"date": "2026-01-02 00:00:00"},
				"department": map[string]any{"id": 2}, "location": map[string]any{"id": 3},
			}}
		case "/api/v1/departments":
			rows = []any{map[string]any{"id": 2, "name": "Department A"}}
		case "/api/v1/locations":
			rows = []any{map[string]any{"id": 3, "name": "Location A"}}
		case "/api/v1/statuslabels":
			rows = []any{
				map[string]any{"id": 2, "name": "Ready", "type": "deployable"},
				map[string]any{"id": 3, "name": "Archived", "type": "archived"},
			}
		case "/api/v1/manufacturers":
			rows = []any{map[string]any{"id": 4, "name": "Example Computers"}}
		case "/api/v1/fields":
			rows = []any{map[string]any{"id": 5, "name": "Managed By", "db_column_name": "_snipeit_managed_by_5"}}
		case "/api/v1/hardware":
			if request.URL.Query().Get("status") == "3" {
				rows = []any{assetFixture(12, "SERIAL-ARCHIVED", "Archived", "archived")}
			} else {
				rows = []any{assetFixture(11, " serial-present ", "Ready", "deployable")}
			}
		default:
			http.NotFound(response, request)
			return
		}
		writeJSON(t, response, map[string]any{"total": len(rows), "rows": rows})
	}))
	t.Cleanup(server.Close)
	client := newClient(server.URL+"/api/v1", "fixture-key", server.Client())

	snapshot, err := client.Snapshot(context.Background(), "Managed By")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snapshot.Users["casey@example.invalid"].StartDate, "2026-01-02"; got != want {
		t.Errorf("start date = %q, want %q", got, want)
	}
	asset := snapshot.Assets["SERIAL-PRESENT"]
	if got, want := asset.ManagedBy, "primary"; got != want {
		t.Errorf("managed by = %q, want %q", got, want)
	}
	if !snapshot.Assets["SERIAL-ARCHIVED"].Archived {
		t.Error("archived asset was not classified")
	}
	if got, want := snapshot.ManagedBy.DBColumn, "_snipeit_managed_by_5"; got != want {
		t.Errorf("custom field column = %q, want %q", got, want)
	}
	if got := snapshot.Departments.Entries()["department a"]; len(got) != 1 || got[0].ID != 2 {
		t.Errorf("department entries = %#v", got)
	}
}

func TestGetAllRejectsPartialPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(t, response, map[string]any{"total": 2, "rows": []any{}})
	}))
	t.Cleanup(server.Close)
	client := newClient(server.URL, "fixture-key", server.Client())
	_, err := client.getAll(context.Background(), "users", nil)
	if err == nil || !strings.Contains(err.Error(), "ended at 0 of 2") {
		t.Fatalf("getAll error = %v, want incomplete pagination error", err)
	}
}

func TestGetAllRejectsMissingEnvelope(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		writeJSON(t, response, map[string]any{})
	}))
	t.Cleanup(server.Close)
	client := newClient(server.URL, "token", server.Client())
	_, err := client.getAll(context.Background(), "hardware", nil)
	if err == nil || !strings.Contains(err.Error(), "total and rows are required") {
		t.Fatalf("getAll error = %v, want missing-envelope error", err)
	}
}

func TestWriteOperationsUseSparseSnipeContracts(t *testing.T) {
	requests := make(map[string]map[string]any)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode %s: %v", request.URL.Path, err)
			return
		}
		requests[request.Method+" "+request.URL.Path] = payload
		body := map[string]any{"status": "success"}
		if request.Method == http.MethodPost && request.URL.Path == "/api/v1/users" {
			body["payload"] = map[string]any{"id": 9}
		}
		writeJSON(t, response, body)
	}))
	t.Cleanup(server.Close)
	client := newClient(server.URL+"/api/v1", "fixture-key", server.Client())
	ctx := context.Background()
	name := "RENAMED"
	managedBy := "primary"
	if err := client.PatchAsset(ctx, 42, domain.AssetPatch{Name: &name, ManagedBy: &managedBy}, "_snipeit_managed_by_5"); err != nil {
		t.Fatal(err)
	}
	if err := client.CheckinAsset(ctx, 42); err != nil {
		t.Fatal(err)
	}
	location := time.FixedZone("fixture", 10*60*60)
	if err := client.CheckoutAsset(ctx, 42, 9, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), location); err != nil {
		t.Fatal(err)
	}
	email := "casey@example.invalid"
	if _, err := client.CreateUser(ctx, domain.UserPatch{GivenName: &name, Email: &email}); err != nil {
		t.Fatal(err)
	}

	patch := requests["PATCH /api/v1/hardware/42"]
	wantPatch := map[string]any{"name": "RENAMED", "_snipeit_managed_by_5": "primary"}
	if !reflect.DeepEqual(patch, wantPatch) {
		t.Errorf("asset patch = %#v, want %#v", patch, wantPatch)
	}
	checkout := requests["POST /api/v1/hardware/42/checkout"]
	if got, want := checkout["checkout_at"], "2026-01-02 10:00:00"; got != want {
		t.Errorf("checkout_at = %#v, want %#v", got, want)
	}
	create := requests["POST /api/v1/users"]
	if create["activated"] != false || create["password"] == "" || create["password"] != create["password_confirmation"] {
		t.Errorf("create payload does not contain inactive matching password fields: %#v", create)
	}
}

func assetFixture(id int, serial, status, statusType string) map[string]any {
	return map[string]any{
		"id": id, "asset_tag": "TAG", "serial": serial, "name": "CURRENT",
		"assigned_to":   map[string]any{"id": 7, "type": "user", "email": "casey@example.invalid"},
		"status_label":  map[string]any{"id": id, "name": status, "type": statusType},
		"manufacturer":  map[string]any{"name": "Example Computers"},
		"custom_fields": map[string]any{"Managed By": map[string]any{"value": "primary"}},
	}
}

func writeJSON(t *testing.T, response http.ResponseWriter, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
