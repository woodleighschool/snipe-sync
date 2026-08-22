package jamf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientCachesAndRefreshesOAuthToken(t *testing.T) {
	t.Parallel()
	var tokenRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/oauth/token" {
			tokenRequests.Add(1)
			if got, want := request.Header.Get("Content-Type"), "application/x-www-form-urlencoded"; got != want {
				t.Errorf("token content type = %q, want %q", got, want)
			}
			if err := request.ParseForm(); err != nil {
				t.Fatalf("parse token form: %v", err)
			}
			wantForm := url.Values{
				"grant_type":    {"client_credentials"},
				"client_id":     {"fixture-client"},
				"client_secret": {"fixture-secret"},
			}
			if got := request.Form.Encode(); got != wantForm.Encode() {
				t.Errorf("token form = %q, want %q", got, wantForm.Encode())
			}
			writeJSON(t, response, http.StatusOK, map[string]any{
				"access_token": fmt.Sprintf("token-%d", tokenRequests.Load()),
				"expires_in":   120,
			})
			return
		}
		if got := request.Header.Get("Authorization"); got == "" {
			t.Error("authorization header is empty")
		}
		writeJSON(t, response, http.StatusOK, map[string]any{"totalCount": 0, "results": []any{}})
	}))
	t.Cleanup(server.Close)

	now := time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC)
	client := newClient(server.URL, "fixture-client", "fixture-secret", server.Client(), func() time.Time {
		return now
	})
	if _, err := client.ListComputers(context.Background(), "jamf"); err != nil {
		t.Fatalf("first ListComputers() error = %v", err)
	}
	if _, err := client.ListComputers(context.Background(), "jamf"); err != nil {
		t.Fatalf("second ListComputers() error = %v", err)
	}
	if got := tokenRequests.Load(); got != 1 {
		t.Fatalf("token requests = %d, want 1", got)
	}

	now = now.Add(91 * time.Second)
	if _, err := client.ListComputers(context.Background(), "jamf"); err != nil {
		t.Fatalf("third ListComputers() error = %v", err)
	}
	if got := tokenRequests.Load(); got != 2 {
		t.Errorf("token requests after expiry leeway = %d, want 2", got)
	}
}

func TestClientRetriesOnceWithFreshTokenAfterUnauthorized(t *testing.T) {
	t.Parallel()
	var tokenRequests atomic.Int32
	var inventoryRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/oauth/token":
			current := tokenRequests.Add(1)
			writeJSON(t, response, http.StatusOK, map[string]any{
				"access_token": fmt.Sprintf("token-%d", current),
				"expires_in":   600,
			})
		case "/api/v4/computers-inventory":
			current := inventoryRequests.Add(1)
			if current == 1 {
				writeJSON(t, response, http.StatusUnauthorized, map[string]string{"error": "expired"})
				return
			}
			if got, want := request.Header.Get("Authorization"), "Bearer token-2"; got != want {
				t.Errorf("retried authorization = %q, want %q", got, want)
			}
			writeJSON(t, response, http.StatusOK, map[string]any{"totalCount": 0, "results": []any{}})
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	client := newClient(server.URL, "fixture-client", "fixture-secret", server.Client(), time.Now)
	if _, err := client.ListComputers(context.Background(), "jamf"); err != nil {
		t.Fatalf("ListComputers() error = %v", err)
	}
	if got := tokenRequests.Load(); got != 2 {
		t.Errorf("token requests = %d, want 2", got)
	}
	if got := inventoryRequests.Load(); got != 2 {
		t.Errorf("inventory requests = %d, want 2", got)
	}
}

func newAuthenticatedServer(
	t *testing.T,
	handler func(http.ResponseWriter, *http.Request),
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/oauth/token" {
			writeJSON(t, response, http.StatusOK, map[string]any{
				"access_token": "fixture-token",
				"expires_in":   600,
			})
			return
		}
		if got, want := request.Header.Get("Authorization"), "Bearer fixture-token"; got != want {
			t.Errorf("authorization = %q, want %q", got, want)
		}
		handler(response, request)
	}))
}

func writeJSON(t *testing.T, response http.ResponseWriter, status int, value any) {
	t.Helper()
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(response).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
