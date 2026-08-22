package microsoft

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestListEntraUsersFiltersCandidatesAndPreservesGroupFailure(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/v1.0/users" {
			if got, want := request.URL.Query().Get("$top"), "999"; got != want {
				t.Errorf("$top = %q, want %q", got, want)
			}
			return jsonResponse(http.StatusOK, map[string]any{"value": []map[string]any{
				{"id": "user-1", "givenName": "Casey", "surname": "Unit", "userPrincipalName": " CASEY@EXAMPLE.INVALID ", "mailNickname": "Casey", "department": "Staff", "createdDateTime": "2026-01-02T03:04:05Z"},
				{"id": "user-2", "givenName": "Taylor", "userPrincipalName": "taylor@example.invalid", "mailNickname": "taylor"},
				{"id": "guest", "givenName": "Guest", "userPrincipalName": "guest_example.com#EXT#@example.invalid"},
				{"id": "external", "givenName": "External", "userPrincipalName": "external@other.invalid"},
			}}), nil
		}
		if !strings.HasSuffix(request.URL.Path, "/checkMemberGroups") {
			return nil, fmt.Errorf("unexpected path %s", request.URL.Path)
		}
		if strings.Contains(request.URL.Path, "user-2") {
			return jsonResponse(http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "Unavailable", "message": "try later"}}), nil
		}
		var payload struct {
			GroupIDs []string `json:"groupIds"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if got, want := payload.GroupIDs, []string{"group-1"}; !reflect.DeepEqual(got, want) {
			t.Errorf("group IDs = %v, want %v", got, want)
		}
		return jsonResponse(http.StatusOK, map[string]any{"value": []string{"group-1"}}), nil
	})
	client := newTestClient(t, transport)

	users, warnings, err := client.ListEntraUsers(context.Background(), []string{"example.invalid"}, map[string][]string{"staff": {"group-1"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(users), 2; got != want {
		t.Fatalf("users = %d, want %d", got, want)
	}
	if got, want := users[0].UserPrincipalName, "casey@example.invalid"; got != want {
		t.Errorf("first user = %q, want %q", got, want)
	}
	if got, want := users[0].Groups, []string{"staff"}; !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
	if !users[0].GroupsComplete {
		t.Error("successful group snapshot is incomplete")
	}
	if users[1].GroupsComplete {
		t.Error("failed group snapshot is complete")
	}
	if got, want := len(warnings), 1; got != want || warnings[0].UserPrincipalName != "taylor@example.invalid" {
		t.Fatalf("warnings = %#v, want one Taylor warning", warnings)
	}
}

func TestListEntraUsersRejectsMissingCollectionValue(t *testing.T) {
	client := newTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"@odata.context": "fixture"}), nil
	}))
	_, _, err := client.ListEntraUsers(context.Background(), []string{"example.invalid"}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "response is missing value") {
		t.Fatalf("ListEntraUsers error = %v, want missing-value error", err)
	}
}
