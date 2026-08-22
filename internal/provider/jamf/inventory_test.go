package jamf

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

func TestListComputersUsesV4BulkInventoryAndPaging(t *testing.T) {
	t.Parallel()
	server := newAuthenticatedServer(t, func(response http.ResponseWriter, request *http.Request) {
		if got, want := request.URL.Path, "/api/v4/computers-inventory"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		assertQueryValues(t, request, map[string][]string{
			"page-size": {"1000"},
			"section":   {"GENERAL", "HARDWARE", "OPERATING_SYSTEM", "USER_AND_LOCATION"},
			"sort":      {"id:asc"},
		})
		if request.URL.Query().Get("page") == "1" {
			writeJSON(t, response, http.StatusOK, map[string]any{
				"totalCount": 2,
				"results": []map[string]any{{
					"id": "computer-2",
					"general": map[string]string{
						"name":        "SECOND",
						"lastCheckIn": "2026-07-30T00:00:00Z",
					},
					"hardware": map[string]string{"serialNumber": "SERIAL-2"},
					"userAndLocation": map[string]string{
						"username": "fixture-user",
					},
				}},
			})
			return
		}
		writeJSON(t, response, http.StatusOK, map[string]any{
			"totalCount": 2,
			"results": []map[string]any{{
				"id": "computer-1",
				"general": map[string]string{
					"name":             "FIRST",
					"platform":         "Mac",
					"lastContact":      "2026-07-31T00:00:00Z",
					"lastCheckIn":      "2026-07-30T00:00:00Z",
					"lastEnrolledDate": "2026-07-01T00:00:00Z",
				},
				"hardware": map[string]string{
					"serialNumber": "SERIAL-1",
					"model":        "FixtureBook",
				},
				"operatingSystem": map[string]string{"version": "26.0"},
				"userAndLocation": map[string]string{
					"email":    "fixture-user@example.invalid",
					"username": "ignored-fixture-user",
				},
			}},
		})
	})
	defer server.Close()

	client := newClient(server.URL, "fixture-client", "fixture-secret", server.Client(), time.Now)
	devices, err := client.ListComputers(context.Background(), "jamf")
	if err != nil {
		t.Fatalf("ListComputers() error = %v", err)
	}
	want := []domain.Device{
		{
			Source:                   "jamf",
			Namespace:                ComputerNamespace,
			ID:                       "computer-1",
			Name:                     "FIRST",
			SerialNumber:             "SERIAL-1",
			Platform:                 "macos",
			EnrolledAt:               time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
			PrimaryUserPrincipalName: "fixture-user@example.invalid",
			Attributes: map[string]string{
				"model":      "FixtureBook",
				"os_version": "26.0",
			},
			LastContactAt: time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC),
		},
		{
			Source:       "jamf",
			Namespace:    ComputerNamespace,
			ID:           "computer-2",
			Name:         "SECOND",
			SerialNumber: "SERIAL-2",
			Platform:     "macos",
			Attributes: map[string]string{
				"model":      "",
				"os_version": "",
			},
			LastContactAt: time.Date(2026, time.July, 30, 0, 0, 0, 0, time.UTC),
		},
	}
	if !reflect.DeepEqual(devices, want) {
		t.Errorf("ListComputers() = %#v, want %#v", devices, want)
	}
}

func TestListMobileDevicesUsesV2BulkInventoryAndSkipsUnsupportedPlatforms(t *testing.T) {
	t.Parallel()
	server := newAuthenticatedServer(t, func(response http.ResponseWriter, request *http.Request) {
		if got, want := request.URL.Path, "/api/v2/mobile-devices/detail"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		assertQueryValues(t, request, map[string][]string{
			"page":               {"0"},
			"page-size":          {"1000"},
			"section":            {"GENERAL", "HARDWARE", "USER_AND_LOCATION"},
			"exception-handling": {"LENIENT"},
			"sort":               {"mobileDeviceId:asc"},
		})
		writeJSON(t, response, http.StatusOK, map[string]any{
			"totalCount": 2,
			"results": []map[string]any{
				{
					"mobileDeviceId": "mobile-1",
					"deviceType":     "iOS",
					"general": map[string]string{
						"displayName":             "FIXTURE-IPAD",
						"lastContactDate":         "2026-07-31T00:00:00Z",
						"lastInventoryUpdateDate": "2026-07-30T00:00:00Z",
						"lastEnrolledDate":        "2026-07-01T00:00:00Z",
						"osVersion":               "26.0",
					},
					"hardware": map[string]string{
						"serialNumber": "MOBILE-SERIAL-1",
						"model":        "FixturePad",
					},
					"userAndLocation": map[string]string{
						"emailAddress": "unit@example.invalid",
						"username":     "ignored-unit",
					},
				},
				{"mobileDeviceId": "tv-1", "deviceType": "tvOS"},
			},
		})
	})
	defer server.Close()

	client := newClient(server.URL, "fixture-client", "fixture-secret", server.Client(), time.Now)
	devices, err := client.ListMobileDevices(context.Background(), "jamf")
	if err != nil {
		t.Fatalf("ListMobileDevices() error = %v", err)
	}
	want := []domain.Device{{
		Source:                   "jamf",
		Namespace:                MobileDeviceNamespace,
		ID:                       "mobile-1",
		Name:                     "FIXTURE-IPAD",
		SerialNumber:             "MOBILE-SERIAL-1",
		Platform:                 "ios",
		EnrolledAt:               time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
		PrimaryUserPrincipalName: "unit@example.invalid",
		Attributes: map[string]string{
			"model":      "FixturePad",
			"os_version": "26.0",
		},
		LastContactAt: time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC),
	}}
	if !reflect.DeepEqual(devices, want) {
		t.Errorf("ListMobileDevices() = %#v, want %#v", devices, want)
	}
}

func TestInventoryRejectsPartialPagination(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		path string
		list func(*Client) error
	}{
		{
			name: "computers",
			path: "/api/v4/computers-inventory",
			list: func(client *Client) error {
				_, err := client.ListComputers(context.Background(), "jamf")
				return err
			},
		},
		{
			name: "mobile devices",
			path: "/api/v2/mobile-devices/detail",
			list: func(client *Client) error {
				_, err := client.ListMobileDevices(context.Background(), "jamf")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newAuthenticatedServer(t, func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Fatalf("path = %q, want %q", request.URL.Path, test.path)
				}
				writeJSON(t, response, http.StatusOK, map[string]any{"totalCount": 2, "results": []any{}})
			})
			defer server.Close()

			client := newClient(server.URL, "fixture-client", "fixture-secret", server.Client(), time.Now)
			err := test.list(client)
			if err == nil || !strings.Contains(err.Error(), "ended at 0 of 2 records") {
				t.Fatalf("inventory error = %v, want partial-pagination error", err)
			}
		})
	}
}

func TestInventoryRejectsMissingEnvelope(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		path string
		list func(*Client) error
	}{
		{
			name: "computers",
			path: "/api/v4/computers-inventory",
			list: func(client *Client) error {
				_, err := client.ListComputers(context.Background(), "jamf")
				return err
			},
		},
		{
			name: "mobile devices",
			path: "/api/v2/mobile-devices/detail",
			list: func(client *Client) error {
				_, err := client.ListMobileDevices(context.Background(), "jamf")
				return err
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := newAuthenticatedServer(t, func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Fatalf("path = %q, want %q", request.URL.Path, test.path)
				}
				writeJSON(t, response, http.StatusOK, map[string]any{})
			})
			defer server.Close()

			client := newClient(server.URL, "fixture-client", "fixture-secret", server.Client(), time.Now)
			err := test.list(client)
			if err == nil || !strings.Contains(err.Error(), "totalCount and results are required") {
				t.Fatalf("inventory error = %v, want missing-envelope error", err)
			}
		})
	}
}

func assertQueryValues(t *testing.T, request *http.Request, want map[string][]string) {
	t.Helper()
	query := request.URL.Query()
	for key, wantValues := range want {
		if got := query[key]; !reflect.DeepEqual(got, wantValues) {
			t.Errorf("query %s = %#v, want %#v", key, got, wantValues)
		}
	}
}
