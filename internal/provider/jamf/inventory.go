package jamf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

const jamfPageSize = 1000

const (
	// ComputerNamespace identifies Jamf's computer inventory ID space.
	ComputerNamespace = "computers"
	// MobileDeviceNamespace identifies Jamf's mobile-device ID space.
	MobileDeviceNamespace = "mobile_devices"
)

// ListComputers returns a provider-neutral snapshot from the Jamf Pro v4 computer inventory API.
func (c *Client) ListComputers(ctx context.Context, source string) ([]domain.Device, error) {
	if err := validateInventoryRequest(c, source); err != nil {
		return nil, err
	}
	var devices []domain.Device
	total := -1
	for page := 0; ; page++ {
		query := inventoryQuery(page, "GENERAL", "HARDWARE", "OPERATING_SYSTEM", "USER_AND_LOCATION")
		query.Set("sort", "id:asc")
		body, err := c.request(ctx, http.MethodGet, "/api/v4/computers-inventory", query, nil, http.StatusOK)
		if err != nil {
			return nil, fmt.Errorf("list Jamf computers: %w", err)
		}
		var result computerSearchResult
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode Jamf computers: %w", err)
		}
		if result.TotalCount == nil || result.Results == nil {
			return nil, fmt.Errorf("decode Jamf computers: totalCount and results are required")
		}
		pageTotal, results := *result.TotalCount, *result.Results
		if err := validatePage("computers", total, pageTotal, len(devices), len(results)); err != nil {
			return nil, err
		}
		if total < 0 {
			total = pageTotal
		}
		for _, computer := range results {
			devices = append(devices, computer.device(source))
		}
		if len(devices) == total {
			break
		}
	}
	return devices, nil
}

// ListMobileDevices returns supported iOS and iPadOS records from the Jamf Pro v2 mobile inventory API.
func (c *Client) ListMobileDevices(ctx context.Context, source string) ([]domain.Device, error) {
	if err := validateInventoryRequest(c, source); err != nil {
		return nil, err
	}
	var devices []domain.Device
	seen := 0
	total := -1
	for page := 0; ; page++ {
		query := inventoryQuery(page, "GENERAL", "HARDWARE", "USER_AND_LOCATION")
		query.Set("exception-handling", "LENIENT")
		query.Set("sort", "mobileDeviceId:asc")
		body, err := c.request(ctx, http.MethodGet, "/api/v2/mobile-devices/detail", query, nil, http.StatusOK)
		if err != nil {
			return nil, fmt.Errorf("list Jamf mobile devices: %w", err)
		}
		var result mobileSearchResult
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("decode Jamf mobile devices: %w", err)
		}
		if result.TotalCount == nil || result.Results == nil {
			return nil, fmt.Errorf("decode Jamf mobile devices: totalCount and results are required")
		}
		pageTotal, results := *result.TotalCount, *result.Results
		if err := validatePage("mobile devices", total, pageTotal, seen, len(results)); err != nil {
			return nil, err
		}
		if total < 0 {
			total = pageTotal
		}
		seen += len(results)
		for _, mobile := range results {
			if !strings.EqualFold(mobile.DeviceType, "ios") {
				continue
			}
			devices = append(devices, mobile.device(source))
		}
		if seen == total {
			break
		}
	}
	return devices, nil
}

func validatePage(kind string, expectedTotal, total, seen, pageRows int) error {
	if total < 0 || total < seen+pageRows {
		return fmt.Errorf("jamf %s pagination returned invalid total %d", kind, total)
	}
	if expectedTotal >= 0 && total != expectedTotal {
		return fmt.Errorf("jamf %s pagination total changed from %d to %d", kind, expectedTotal, total)
	}
	if pageRows == 0 && seen != total {
		return fmt.Errorf("jamf %s pagination ended at %d of %d records", kind, seen, total)
	}
	return nil
}

type computerSearchResult struct {
	TotalCount *int              `json:"totalCount"`
	Results    *[]computerRecord `json:"results"`
}

type computerRecord struct {
	ID      string `json:"id"`
	General struct {
		Name             string `json:"name"`
		Platform         string `json:"platform"`
		ReportDate       string `json:"reportDate"`
		LastCheckIn      string `json:"lastCheckIn"`
		LastContact      string `json:"lastContact"`
		LastEnrolledDate string `json:"lastEnrolledDate"`
	} `json:"general"`
	Hardware struct {
		Model        string `json:"model"`
		SerialNumber string `json:"serialNumber"`
	} `json:"hardware"`
	OperatingSystem struct {
		Version string `json:"version"`
	} `json:"operatingSystem"`
	UserAndLocation struct {
		Email string `json:"email"`
	} `json:"userAndLocation"`
}

func (r computerRecord) device(source string) domain.Device {
	return domain.Device{
		Source:                   source,
		Namespace:                ComputerNamespace,
		ID:                       r.ID,
		Name:                     r.General.Name,
		SerialNumber:             r.Hardware.SerialNumber,
		Platform:                 "macos",
		EnrolledAt:               parseTimestamp(r.General.LastEnrolledDate),
		PrimaryUserPrincipalName: r.UserAndLocation.Email,
		Attributes: map[string]string{
			"model":      r.Hardware.Model,
			"os_version": r.OperatingSystem.Version,
		},
		LastContactAt: parseFirstTimestamp(
			r.General.LastContact,
			r.General.LastCheckIn,
			r.General.ReportDate,
		),
	}
}

type mobileSearchResult struct {
	TotalCount *int            `json:"totalCount"`
	Results    *[]mobileRecord `json:"results"`
}

type mobileRecord struct {
	MobileDeviceID string `json:"mobileDeviceId"`
	DeviceType     string `json:"deviceType"`
	General        struct {
		DisplayName             string `json:"displayName"`
		LastInventoryUpdateDate string `json:"lastInventoryUpdateDate"`
		LastContactDate         string `json:"lastContactDate"`
		LastEnrolledDate        string `json:"lastEnrolledDate"`
		OSVersion               string `json:"osVersion"`
	} `json:"general"`
	Hardware struct {
		SerialNumber string `json:"serialNumber"`
		Model        string `json:"model"`
	} `json:"hardware"`
	UserAndLocation struct {
		EmailAddress string `json:"emailAddress"`
	} `json:"userAndLocation"`
}

func (r mobileRecord) device(source string) domain.Device {
	return domain.Device{
		Source:                   source,
		Namespace:                MobileDeviceNamespace,
		ID:                       r.MobileDeviceID,
		Name:                     r.General.DisplayName,
		SerialNumber:             r.Hardware.SerialNumber,
		Platform:                 "ios",
		EnrolledAt:               parseTimestamp(r.General.LastEnrolledDate),
		PrimaryUserPrincipalName: r.UserAndLocation.EmailAddress,
		Attributes: map[string]string{
			"model":      r.Hardware.Model,
			"os_version": r.General.OSVersion,
		},
		LastContactAt: parseFirstTimestamp(
			r.General.LastContactDate,
			r.General.LastInventoryUpdateDate,
		),
	}
}

func inventoryQuery(page int, sections ...string) url.Values {
	return url.Values{
		"page":      {strconv.Itoa(page)},
		"page-size": {strconv.Itoa(jamfPageSize)},
		"section":   sections,
	}
}

func validateInventoryRequest(client *Client, source string) error {
	if client == nil || client.httpClient == nil {
		return fmt.Errorf("jamf client is required")
	}
	if strings.TrimSpace(source) == "" {
		return fmt.Errorf("device source is required")
	}
	return nil
}

func parseFirstTimestamp(values ...string) time.Time {
	for _, value := range values {
		if parsed := parseTimestamp(value); !parsed.IsZero() {
			return parsed
		}
	}
	return time.Time{}
}

func parseTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return parsed.UTC()
}
