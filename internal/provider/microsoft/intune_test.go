package microsoft

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

func TestListIntuneDevicesUsesSelectedFieldsAndPaging(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1.0/deviceManagement/managedDevices" {
			return nil, fmt.Errorf("unexpected path %s", request.URL.Path)
		}
		if request.URL.Query().Get("$skiptoken") == "next" {
			return jsonResponse(http.StatusOK, map[string]any{"value": []map[string]any{{
				"id": "device-2", "deviceName": "SECOND", "serialNumber": "SERIAL-2",
				"operatingSystem": "Windows", "userPrincipalName": "unit@example.invalid",
			}}}), nil
		}
		if got, want := request.URL.Query().Get("$top"), "999"; got != want {
			t.Errorf("$top = %q, want %q", got, want)
		}
		wantSelect := []string{"id", "deviceName", "serialNumber", "operatingSystem", "osVersion", "userPrincipalName", "model", "enrolledDateTime", "lastSyncDateTime"}
		if got := strings.Split(request.URL.Query().Get("$select"), ","); !reflect.DeepEqual(got, wantSelect) {
			t.Errorf("$select = %#v, want %#v", got, wantSelect)
		}
		return jsonResponse(http.StatusOK, map[string]any{
			"@odata.nextLink": "https://graph.test/v1.0/deviceManagement/managedDevices?$skiptoken=next&$top=999",
			"value": []map[string]any{{
				"id": "device-1", "deviceName": "FIRST", "serialNumber": "SERIAL-1",
				"operatingSystem": "macOS", "osVersion": "26.0", "userId": "opaque-user-id",
				"model": "Fixture Model", "enrolledDateTime": "2026-07-01T00:00:00Z",
				"lastSyncDateTime": "2026-07-31T00:00:00Z",
			}},
		}), nil
	})
	client := newTestClient(t, transport)

	devices, err := client.ListIntuneDevices(context.Background(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Device{
		{
			Source: "primary", Namespace: managedDeviceNamespace, ID: "device-1", Name: "FIRST",
			SerialNumber: "SERIAL-1", Platform: "macos",
			EnrolledAt:    time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
			LastContactAt: time.Date(2026, time.July, 31, 0, 0, 0, 0, time.UTC),
			Attributes:    map[string]string{"model": "Fixture Model", "os_version": "26.0"},
		},
		{
			Source: "primary", Namespace: managedDeviceNamespace, ID: "device-2", Name: "SECOND",
			SerialNumber: "SERIAL-2", Platform: "windows", PrimaryUserPrincipalName: "unit@example.invalid",
			Attributes: map[string]string{"model": "", "os_version": ""},
		},
	}
	if !reflect.DeepEqual(devices, want) {
		t.Errorf("ListIntuneDevices() = %#v, want %#v", devices, want)
	}
}

func TestListIntuneDevicesRejectsMissingCollectionValue(t *testing.T) {
	client := newTestClient(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, map[string]any{"@odata.context": "fixture"}), nil
	}))
	_, err := client.ListIntuneDevices(context.Background(), "primary")
	if err == nil || !strings.Contains(err.Error(), "response is missing value") {
		t.Fatalf("ListIntuneDevices error = %v, want missing-value error", err)
	}
}
