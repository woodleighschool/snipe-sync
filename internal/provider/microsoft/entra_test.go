package microsoft

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestListEntraUserDeltaReadsCompleteRound(t *testing.T) {
	requests := 0
	client := newTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		switch requests {
		case 1:
			if got, want := request.URL.Path, "/v1.0/users/delta()"; got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
			selectFields := strings.Split(request.URL.Query().Get("$select"), ",")
			for _, field := range []string{"id", "givenName", "surname", "userPrincipalName", "mailNickname", "department", "createdDateTime"} {
				if !slices.Contains(selectFields, field) {
					t.Errorf("$select = %v, missing %q", selectFields, field)
				}
			}
			return jsonResponse(http.StatusOK, map[string]any{
				"value": []map[string]any{
					{"id": "user-1", "givenName": " Casey ", "surname": " Unit ", "userPrincipalName": " CASEY@EXAMPLE.INVALID ", "mailNickname": " Casey ", "department": " Department A ", "createdDateTime": "2026-01-02T03:04:05Z"},
					{"id": "user-2", "@removed": map[string]string{"reason": "deleted"}},
				},
				"@odata.nextLink": "https://graph.test/v1.0/users/delta?$skiptoken=next",
			}), nil
		case 2:
			if got, want := request.URL.Query().Get("$skiptoken"), "next"; got != want {
				t.Fatalf("$skiptoken = %q, want %q", got, want)
			}
			return jsonResponse(http.StatusOK, map[string]any{
				"value":            []map[string]any{{"id": "user-3", "givenName": "Taylor", "userPrincipalName": "taylor@example.invalid"}},
				"@odata.deltaLink": "https://graph.test/v1.0/users/delta?$deltatoken=current",
			}), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
	}))

	delta, err := client.ListEntraUserDelta(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(delta.Changes), 3; got != want {
		t.Fatalf("changes = %d, want %d", got, want)
	}
	if got, want := delta.Changes[0].User.UserPrincipalName, "casey@example.invalid"; got != want {
		t.Errorf("user principal name = %q, want %q", got, want)
	}
	if got, want := delta.Changes[0].User.GivenName, "Casey"; got != want {
		t.Errorf("given name = %q, want %q", got, want)
	}
	if !delta.Changes[1].Removed {
		t.Error("removed user was not marked removed")
	}
	if got, want := delta.DeltaLink, "https://graph.test/v1.0/users/delta?$deltatoken=current"; got != want {
		t.Errorf("delta link = %q, want %q", got, want)
	}
}

func TestListEntraUserDeltaUsesStoredLink(t *testing.T) {
	wantLink := "https://graph.test/v1.0/users/delta?$deltatoken=stored"
	client := newTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if got := request.URL.String(); got != wantLink {
			t.Fatalf("URL = %q, want %q", got, wantLink)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"value":            []map[string]any{},
			"@odata.deltaLink": "https://graph.test/v1.0/users/delta?$deltatoken=next",
		}), nil
	}))

	delta, err := client.ListEntraUserDelta(context.Background(), wantLink)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := delta.DeltaLink, "https://graph.test/v1.0/users/delta?$deltatoken=next"; got != want {
		t.Errorf("delta link = %q, want %q", got, want)
	}
}

func TestListEntraUserDeltaRejectsIncompleteRound(t *testing.T) {
	tests := map[string]map[string]any{
		"missing value":      {"@odata.deltaLink": "https://graph.test/delta"},
		"missing delta link": {"value": []map[string]any{}},
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			client := newTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusOK, body), nil
			}))
			_, err := client.ListEntraUserDelta(context.Background(), "")
			if err == nil {
				t.Fatal("ListEntraUserDelta succeeded")
			}
		})
	}
}

func TestListEntraUserDeltaClassifiesExpiredLink(t *testing.T) {
	client := newTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusGone, map[string]any{
			"error": map[string]string{"code": "syncStateNotFound", "message": "expired"},
		}), nil
	}))

	_, err := client.ListEntraUserDelta(context.Background(), "https://graph.test/v1.0/users/delta?$deltatoken=expired")
	if !errors.Is(err, ErrDeltaExpired) {
		t.Fatalf("ListEntraUserDelta error = %v, want ErrDeltaExpired", err)
	}
}

func TestListEntraGroupUserIDsReadsTransitivePages(t *testing.T) {
	requests := 0
	client := newTestClient(t, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if got, want := request.Header.Get("ConsistencyLevel"), "eventual"; got != want {
			t.Errorf("ConsistencyLevel = %q, want %q", got, want)
		}
		switch requests {
		case 1:
			if got, want := request.URL.Path, "/v1.0/groups/group-1/transitiveMembers/graph.user"; got != want {
				t.Fatalf("path = %q, want %q", got, want)
			}
			query, _ := url.QueryUnescape(request.URL.RawQuery)
			for _, value := range []string{"$count=true", "$select=id", "$top=999"} {
				if !strings.Contains(query, value) {
					t.Errorf("query = %q, missing %q", query, value)
				}
			}
			return jsonResponse(http.StatusOK, map[string]any{
				"value":           []map[string]any{{"id": "user-2"}, {"id": "user-1"}},
				"@odata.nextLink": "https://graph.test/v1.0/groups/group-1/transitiveMembers/graph.user?$skiptoken=next",
			}), nil
		case 2:
			return jsonResponse(http.StatusOK, map[string]any{
				"value": []map[string]any{{"id": "user-2"}, {"id": "user-3"}},
			}), nil
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
	}))

	userIDs, err := client.ListEntraGroupUserIDs(context.Background(), "group-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"user-1", "user-2", "user-3"}; !slices.Equal(userIDs, want) {
		t.Errorf("user IDs = %v, want %v", userIDs, want)
	}
}
