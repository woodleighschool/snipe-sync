package app

import (
	"context"
	"fmt"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/provider/jamf"
	"github.com/woodleighschool/snipe-sync/internal/provider/microsoft"
	"github.com/woodleighschool/snipe-sync/internal/provider/snipe"
)

const groupLookupConcurrency = 8

// Build creates provider clients and a service from validated configuration.
func Build(cfg *config.Config) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	microsoftClients := make(map[string]*microsoft.Client)
	jamfClients := make(map[string]*jamf.Client)
	getMicrosoft := func(name string) (*microsoft.Client, error) {
		if client := microsoftClients[name]; client != nil {
			return client, nil
		}
		connection := cfg.Connections[name]
		client, err := microsoft.NewClient(microsoft.Config{
			TenantID: connection.TenantID, ClientID: connection.ClientID,
			ClientSecret: connection.ClientSecret, BaseURL: connection.BaseURL,
		})
		if err != nil {
			return nil, fmt.Errorf("create Microsoft connection %q: %w", name, err)
		}
		microsoftClients[name] = client
		return client, nil
	}
	getJamf := func(name string) (*jamf.Client, error) {
		if client := jamfClients[name]; client != nil {
			return client, nil
		}
		connection := cfg.Connections[name]
		client, err := jamf.NewClient(jamf.Config{
			URL: connection.URL, ClientID: connection.ClientID, ClientSecret: connection.ClientSecret,
		})
		if err != nil {
			return nil, fmt.Errorf("create Jamf connection %q: %w", name, err)
		}
		jamfClients[name] = client
		return client, nil
	}

	identityClient, err := getMicrosoft(cfg.Identity.Connection)
	if err != nil {
		return nil, err
	}
	identity := &entraSource{client: identityClient, domains: cfg.Identity.Domains, groups: cfg.Identity.Groups}
	sources := make([]DeviceSource, 0, len(cfg.Devices))
	for _, source := range cfg.Devices {
		switch source.Type {
		case "intune":
			client, clientErr := getMicrosoft(source.Connection)
			if clientErr != nil {
				return nil, clientErr
			}
			sources = append(sources, &intuneSource{name: source.Name, client: client})
		case "jamf":
			client, clientErr := getJamf(source.Connection)
			if clientErr != nil {
				return nil, clientErr
			}
			sources = append(sources, &jamfSource{name: source.Name, client: client})
		default:
			return nil, fmt.Errorf("device source type %q is not supported", source.Type)
		}
	}
	targetConnection := cfg.Connections[cfg.Target.Connection]
	targetClient, err := snipe.NewClient(snipe.Config{URL: targetConnection.URL, APIKey: targetConnection.APIKey})
	if err != nil {
		return nil, fmt.Errorf("create Snipe connection %q: %w", cfg.Target.Connection, err)
	}
	return New(cfg, identity, sources, &snipeTarget{client: targetClient})
}

type entraSource struct {
	client  *microsoft.Client
	domains []string
	groups  map[string][]string
}

func (s *entraSource) ListUsers(ctx context.Context) ([]domain.User, []string, error) {
	users, enrichmentWarnings, err := s.client.ListEntraUsers(ctx, s.domains, s.groups, groupLookupConcurrency)
	warnings := make([]string, len(enrichmentWarnings))
	for index, warning := range enrichmentWarnings {
		warnings[index] = warning.Error()
	}
	return users, warnings, err
}

type intuneSource struct {
	name   string
	client *microsoft.Client
}

func (s *intuneSource) Name() string {
	return s.name
}

func (s *intuneSource) ListDevices(ctx context.Context) ([]domain.Device, error) {
	return s.client.ListIntuneDevices(ctx, s.name)
}

type jamfSource struct {
	name   string
	client *jamf.Client
}

type snipeTarget struct {
	client *snipe.Client
}

func (t *snipeTarget) Snapshot(ctx context.Context, managedByLabel string) (*TargetSnapshot, error) {
	snapshot, err := t.client.Snapshot(ctx, managedByLabel)
	if err != nil {
		return nil, err
	}
	return &TargetSnapshot{
		Users: snapshot.Users, Assets: snapshot.Assets,
		Departments: catalogIDs(snapshot.Departments), Locations: catalogIDs(snapshot.Locations),
		Statuses: catalogIDs(snapshot.Statuses), Manufacturers: catalogCounts(snapshot.Manufacturers),
		ManagedByColumn: snapshot.ManagedBy.DBColumn,
	}, nil
}

func (t *snipeTarget) CreateUser(ctx context.Context, patch domain.UserPatch) (int64, error) {
	return t.client.CreateUser(ctx, patch)
}

func (t *snipeTarget) PatchUser(ctx context.Context, userID int64, patch domain.UserPatch) error {
	return t.client.PatchUser(ctx, userID, patch)
}

func (t *snipeTarget) PatchAsset(ctx context.Context, assetID int64, patch domain.AssetPatch, managedByColumn string) error {
	return t.client.PatchAsset(ctx, assetID, patch, managedByColumn)
}

func (t *snipeTarget) CheckinAsset(ctx context.Context, assetID int64) error {
	return t.client.CheckinAsset(ctx, assetID)
}

func (t *snipeTarget) CheckoutAsset(
	ctx context.Context,
	assetID, userID int64,
	checkoutAt time.Time,
	location *time.Location,
) error {
	return t.client.CheckoutAsset(ctx, assetID, userID, checkoutAt, location)
}

func catalogIDs(catalog snipe.Catalog) map[string][]int64 {
	result := make(map[string][]int64)
	for name, entries := range catalog.Entries() {
		for _, entry := range entries {
			result[name] = append(result[name], entry.ID)
		}
	}
	return result
}

func catalogCounts(catalog snipe.Catalog) map[string]int {
	result := make(map[string]int)
	for name, entries := range catalog.Entries() {
		result[name] = len(entries)
	}
	return result
}

func (s *jamfSource) Name() string {
	return s.name
}

func (s *jamfSource) ListDevices(ctx context.Context) ([]domain.Device, error) {
	computers, err := s.client.ListComputers(ctx, s.name)
	if err != nil {
		return nil, err
	}
	mobileDevices, err := s.client.ListMobileDevices(ctx, s.name)
	if err != nil {
		return nil, err
	}
	return append(computers, mobileDevices...), nil
}
