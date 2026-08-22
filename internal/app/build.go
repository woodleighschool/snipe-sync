package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/provider/jamf"
	"github.com/woodleighschool/snipe-sync/internal/provider/microsoft"
	"github.com/woodleighschool/snipe-sync/internal/provider/snipe"
)

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
	identity := newEntraSource(identityClient, cfg.Identity.Domains, cfg.Identity.Groups)
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

type entraClient interface {
	ListEntraUserDelta(context.Context, string) (microsoft.EntraUserDelta, error)
	ListEntraGroupUserIDs(context.Context, string) ([]string, error)
}

type entraSource struct {
	mu        sync.Mutex
	client    entraClient
	domains   map[string]struct{}
	groups    map[string][]string
	users     map[string]domain.User
	deltaLink string
}

func newEntraSource(client entraClient, domains []string, groups map[string][]string) *entraSource {
	domainSet := make(map[string]struct{}, len(domains))
	for _, value := range domains {
		domainSet[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	return &entraSource{
		client: client, domains: domainSet, groups: groups,
		users: make(map[string]domain.User),
	}
}

func (s *entraSource) ListUsers(ctx context.Context) ([]domain.User, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delta, err := s.client.ListEntraUserDelta(ctx, s.deltaLink)
	reset := s.deltaLink == ""
	if errors.Is(err, microsoft.ErrDeltaExpired) {
		delta, err = s.client.ListEntraUserDelta(ctx, "")
		reset = true
	}
	if err != nil {
		return nil, nil, err
	}

	usersByID := make(map[string]domain.User, len(s.users)+len(delta.Changes))
	if !reset {
		maps.Copy(usersByID, s.users)
	}
	for _, change := range delta.Changes {
		if change.Removed || !s.includes(change.User) {
			delete(usersByID, change.User.ID)
			continue
		}
		usersByID[change.User.ID] = change.User
	}

	groupsByUserID, err := s.listGroups(ctx)
	if err != nil {
		return nil, nil, err
	}
	users := make([]domain.User, 0, len(usersByID))
	for id, user := range usersByID {
		user.Groups = append([]string(nil), groupsByUserID[id]...)
		user.GroupsComplete = true
		usersByID[id] = user
		users = append(users, user)
	}
	sort.Slice(users, func(left, right int) bool {
		return users[left].UserPrincipalName < users[right].UserPrincipalName
	})

	s.users = usersByID
	s.deltaLink = delta.DeltaLink
	return users, nil, nil
}

func (s *entraSource) includes(user domain.User) bool {
	if user.UserPrincipalName == "" || strings.Contains(user.UserPrincipalName, "#ext#") || user.GivenName == "" {
		return false
	}
	separator := strings.LastIndexByte(user.UserPrincipalName, '@')
	if separator < 0 {
		return false
	}
	_, ok := s.domains[user.UserPrincipalName[separator+1:]]
	return ok
}

func (s *entraSource) listGroups(ctx context.Context) (map[string][]string, error) {
	aliasesByGroupID := make(map[string][]string)
	for alias, groupIDs := range s.groups {
		for _, groupID := range groupIDs {
			groupID = strings.TrimSpace(groupID)
			aliasesByGroupID[groupID] = append(aliasesByGroupID[groupID], alias)
		}
	}
	groupIDs := make([]string, 0, len(aliasesByGroupID))
	for groupID := range aliasesByGroupID {
		sort.Strings(aliasesByGroupID[groupID])
		groupIDs = append(groupIDs, groupID)
	}
	sort.Strings(groupIDs)

	type groupMembers struct {
		groupID string
		userIDs []string
	}
	results := make([]groupMembers, len(groupIDs))
	group, groupCtx := errgroup.WithContext(ctx)
	for index, groupID := range groupIDs {
		group.Go(func() error {
			userIDs, err := s.client.ListEntraGroupUserIDs(groupCtx, groupID)
			if err != nil {
				return err
			}
			results[index] = groupMembers{groupID: groupID, userIDs: userIDs}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	aliasesByUserID := make(map[string]map[string]struct{})
	for _, result := range results {
		for _, userID := range result.userIDs {
			aliases := aliasesByUserID[userID]
			if aliases == nil {
				aliases = make(map[string]struct{})
				aliasesByUserID[userID] = aliases
			}
			for _, alias := range aliasesByGroupID[result.groupID] {
				aliases[alias] = struct{}{}
			}
		}
	}
	groupsByUserID := make(map[string][]string, len(aliasesByUserID))
	for userID, aliasSet := range aliasesByUserID {
		aliases := make([]string, 0, len(aliasSet))
		for alias := range aliasSet {
			aliases = append(aliases, alias)
		}
		sort.Strings(aliases)
		groupsByUserID[userID] = aliases
	}
	return groupsByUserID, nil
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
