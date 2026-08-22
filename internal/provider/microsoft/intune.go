package microsoft

import (
	"context"
	"fmt"
	"strings"

	devicemanagement "github.com/microsoftgraph/msgraph-sdk-go/devicemanagement"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

const managedDeviceNamespace = "managed_devices"

// ListIntuneDevices returns a provider-neutral snapshot using one paged managedDevices query.
func (c *Client) ListIntuneDevices(ctx context.Context, source string) ([]domain.Device, error) {
	if c == nil || c.graph == nil {
		return nil, fmt.Errorf("graph client is required")
	}
	if source == "" {
		return nil, fmt.Errorf("device source is required")
	}
	page, err := c.graph.DeviceManagement().ManagedDevices().Get(
		ctx,
		&devicemanagement.ManagedDevicesRequestBuilderGetRequestConfiguration{
			QueryParameters: &devicemanagement.ManagedDevicesRequestBuilderGetQueryParameters{
				Select: []string{
					"id",
					"deviceName",
					"serialNumber",
					"operatingSystem",
					"osVersion",
					"userPrincipalName",
					"model",
					"enrolledDateTime",
					"lastSyncDateTime",
				},
				Top: new(graphPageSize),
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Intune devices: %w", err)
	}
	if page == nil {
		return nil, fmt.Errorf("list Intune devices: Graph returned no response")
	}

	var devices []domain.Device
	seenLinks := make(map[string]struct{})
	for {
		pageDevices := page.GetValue()
		if pageDevices == nil {
			return nil, fmt.Errorf("list Intune devices: Graph response is missing value")
		}
		for _, device := range pageDevices {
			converted := domain.Device{
				Source:                   source,
				Namespace:                managedDeviceNamespace,
				ID:                       dereference(device.GetId()),
				Name:                     dereference(device.GetDeviceName()),
				SerialNumber:             dereference(device.GetSerialNumber()),
				Platform:                 normalizePlatform(dereference(device.GetOperatingSystem())),
				PrimaryUserPrincipalName: dereference(device.GetUserPrincipalName()),
				Attributes: map[string]string{
					"model":      dereference(device.GetModel()),
					"os_version": dereference(device.GetOsVersion()),
				},
			}
			if enrolledAt := device.GetEnrolledDateTime(); enrolledAt != nil {
				converted.EnrolledAt = enrolledAt.UTC()
			}
			if lastSeenAt := device.GetLastSyncDateTime(); lastSeenAt != nil {
				converted.LastContactAt = lastSeenAt.UTC()
			}
			devices = append(devices, converted)
		}
		nextLink := page.GetOdataNextLink()
		if nextLink == nil || *nextLink == "" {
			break
		}
		if _, duplicate := seenLinks[*nextLink]; duplicate {
			return nil, fmt.Errorf("page Intune devices: Graph repeated next link")
		}
		seenLinks[*nextLink] = struct{}{}
		page, err = devicemanagement.NewManagedDevicesRequestBuilder(*nextLink, c.graph.GetAdapter()).Get(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("page Intune devices: %w", err)
		}
		if page == nil {
			return nil, fmt.Errorf("page Intune devices: Graph returned no response")
		}
	}
	return devices, nil
}

func normalizePlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "ios", "ipados":
		return "ios"
	case "macos", "mac os x", "osx":
		return "macos"
	case "windows":
		return "windows"
	default:
		return strings.ToLower(strings.TrimSpace(platform))
	}
}
