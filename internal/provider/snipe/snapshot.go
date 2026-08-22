package snipe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/sync/errgroup"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

// NamedID is one human-readable Snipe metadata value.
type NamedID struct {
	ID       int64
	Archived bool
}

// Catalog resolves case-insensitive human-readable Snipe metadata names.
type Catalog struct {
	values map[string][]NamedID
}

// Entries returns a copy of the case-normalized metadata index.
func (c Catalog) Entries() map[string][]NamedID {
	entries := make(map[string][]NamedID, len(c.values))
	for name, values := range c.values {
		entries[name] = append([]NamedID(nil), values...)
	}
	return entries
}

// CustomField identifies a Snipe custom field and its generated request column.
type CustomField struct {
	DBColumn string
}

// Snapshot is one complete normalized Snipe inventory and metadata view.
type Snapshot struct {
	Users         map[string]domain.TargetUser
	Assets        map[string]domain.Asset
	Departments   Catalog
	Locations     Catalog
	Statuses      Catalog
	Manufacturers Catalog
	ManagedBy     CustomField
}

// Snapshot reads every target collection needed to build a safe plan.
func (c *Client) Snapshot(ctx context.Context, managedByLabel string) (*Snapshot, error) {
	var (
		users         map[string]domain.TargetUser
		departments   Catalog
		locations     Catalog
		statuses      Catalog
		manufacturers Catalog
		managedBy     CustomField
	)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		users, err = c.listUsers(groupCtx)
		return err
	})
	group.Go(func() error {
		var err error
		departments, err = c.listCatalog(groupCtx, "departments", false)
		return err
	})
	group.Go(func() error {
		var err error
		locations, err = c.listCatalog(groupCtx, "locations", false)
		return err
	})
	group.Go(func() error {
		var err error
		statuses, err = c.listCatalog(groupCtx, "statuslabels", true)
		return err
	})
	group.Go(func() error {
		var err error
		manufacturers, err = c.listCatalog(groupCtx, "manufacturers", false)
		return err
	})
	group.Go(func() error {
		var err error
		managedBy, err = c.findCustomField(groupCtx, managedByLabel)
		return err
	})
	if err := group.Wait(); err != nil {
		return nil, err
	}
	assets, err := c.listAssets(ctx, statuses, managedByLabel)
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Users:         users,
		Assets:        assets,
		Departments:   departments,
		Locations:     locations,
		Statuses:      statuses,
		Manufacturers: manufacturers,
		ManagedBy:     managedBy,
	}, nil
}

func (c *Client) listUsers(ctx context.Context) (map[string]domain.TargetUser, error) {
	rows, err := c.getAll(ctx, "users", nil)
	if err != nil {
		return nil, fmt.Errorf("list Snipe users: %w", err)
	}
	users := make(map[string]domain.TargetUser, len(rows))
	for _, row := range rows {
		var raw struct {
			ID         int64           `json:"id"`
			Username   string          `json:"username"`
			Email      string          `json:"email"`
			GivenName  string          `json:"first_name"`
			Surname    string          `json:"last_name"`
			StartDate  json.RawMessage `json:"start_date"`
			Department *struct {
				ID int64 `json:"id"`
			} `json:"department"`
			Location *struct {
				ID int64 `json:"id"`
			} `json:"location"`
		}
		if err := json.Unmarshal(row, &raw); err != nil {
			return nil, fmt.Errorf("decode Snipe user: %w", err)
		}
		email := strings.ToLower(strings.TrimSpace(raw.Email))
		if email == "" {
			continue
		}
		if _, exists := users[email]; exists {
			return nil, fmt.Errorf("snipe user email %q is duplicated", email)
		}
		user := domain.TargetUser{
			ID:        raw.ID,
			Username:  strings.TrimSpace(raw.Username),
			Email:     email,
			GivenName: cleanHTML(raw.GivenName),
			Surname:   cleanHTML(raw.Surname),
			StartDate: decodeStartDate(raw.StartDate),
		}
		if raw.Department != nil {
			user.DepartmentID = raw.Department.ID
		}
		if raw.Location != nil {
			user.LocationID = raw.Location.ID
		}
		users[email] = user
	}
	return users, nil
}

func (c *Client) listCatalog(ctx context.Context, path string, statuses bool) (Catalog, error) {
	rows, err := c.getAll(ctx, path, nil)
	if err != nil {
		return Catalog{}, fmt.Errorf("list Snipe %s: %w", path, err)
	}
	catalog := Catalog{values: make(map[string][]NamedID, len(rows))}
	for _, row := range rows {
		var raw struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			Type       string `json:"type"`
			StatusType string `json:"status_type"`
			StatusMeta string `json:"status_meta"`
		}
		if err := json.Unmarshal(row, &raw); err != nil {
			return Catalog{}, fmt.Errorf("decode Snipe %s: %w", path, err)
		}
		name := strings.TrimSpace(raw.Name)
		if raw.ID <= 0 || name == "" {
			continue
		}
		archived := false
		if statuses {
			archived = strings.EqualFold(raw.Type, "archived") || strings.EqualFold(raw.StatusType, "archived") || strings.EqualFold(raw.StatusMeta, "archived")
		}
		key := strings.ToLower(name)
		catalog.values[key] = append(catalog.values[key], NamedID{ID: raw.ID, Archived: archived})
	}
	return catalog, nil
}

func (c *Client) findCustomField(ctx context.Context, label string) (CustomField, error) {
	rows, err := c.getAll(ctx, "fields", nil)
	if err != nil {
		return CustomField{}, fmt.Errorf("list Snipe custom fields: %w", err)
	}
	matches := make([]CustomField, 0, 1)
	for _, row := range rows {
		var raw struct {
			Name     string `json:"name"`
			DBColumn string `json:"db_column_name"`
		}
		if err := json.Unmarshal(row, &raw); err != nil {
			return CustomField{}, fmt.Errorf("decode Snipe custom field: %w", err)
		}
		if strings.EqualFold(strings.TrimSpace(raw.Name), strings.TrimSpace(label)) {
			matches = append(matches, CustomField{DBColumn: strings.TrimSpace(raw.DBColumn)})
		}
	}
	if len(matches) == 0 {
		return CustomField{}, fmt.Errorf("snipe custom field %q was not found", label)
	}
	if len(matches) > 1 {
		return CustomField{}, fmt.Errorf("snipe custom field %q is ambiguous", label)
	}
	if matches[0].DBColumn == "" {
		return CustomField{}, fmt.Errorf("snipe custom field %q has no database column", label)
	}
	return matches[0], nil
}

func (c *Client) listAssets(ctx context.Context, statuses Catalog, managedByLabel string) (map[string]domain.Asset, error) {
	rows, err := c.getAll(ctx, "hardware", nil)
	if err != nil {
		return nil, fmt.Errorf("list Snipe assets: %w", err)
	}
	for _, entries := range statuses.values {
		for _, status := range entries {
			if !status.Archived {
				continue
			}
			archivedRows, archivedErr := c.getAll(ctx, "hardware", url.Values{"status": {strconv.FormatInt(status.ID, 10)}})
			if archivedErr != nil {
				return nil, fmt.Errorf("list archived Snipe assets: %w", archivedErr)
			}
			rows = append(rows, archivedRows...)
		}
	}
	assets := make(map[string]domain.Asset, len(rows))
	seenIDs := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		asset, decodeErr := decodeAsset(row, managedByLabel)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if asset.SerialNumber == "" {
			continue
		}
		if _, duplicatePageRow := seenIDs[asset.ID]; duplicatePageRow {
			continue
		}
		seenIDs[asset.ID] = struct{}{}
		if _, duplicateSerial := assets[asset.SerialNumber]; duplicateSerial {
			return nil, fmt.Errorf("snipe asset serial %q is duplicated", asset.SerialNumber)
		}
		assets[asset.SerialNumber] = asset
	}
	return assets, nil
}

func decodeAsset(row json.RawMessage, managedByLabel string) (domain.Asset, error) {
	var raw struct {
		ID           int64  `json:"id"`
		AssetTag     string `json:"asset_tag"`
		SerialNumber string `json:"serial"`
		Name         string `json:"name"`
		AssignedTo   *struct {
			ID    int64  `json:"id"`
			Type  string `json:"type"`
			Email string `json:"email"`
		} `json:"assigned_to"`
		StatusLabel *struct {
			ID         int64  `json:"id"`
			Name       string `json:"name"`
			StatusMeta string `json:"status_meta"`
			Type       string `json:"type"`
		} `json:"status_label"`
		Manufacturer *struct {
			Name string `json:"name"`
		} `json:"manufacturer"`
		CustomFields map[string]struct {
			Value any `json:"value"`
		} `json:"custom_fields"`
	}
	if err := json.Unmarshal(row, &raw); err != nil {
		return domain.Asset{}, fmt.Errorf("decode Snipe asset: %w", err)
	}
	asset := domain.Asset{
		ID:           raw.ID,
		AssetTag:     strings.TrimSpace(raw.AssetTag),
		SerialNumber: strings.ToUpper(strings.TrimSpace(raw.SerialNumber)),
		Name:         strings.TrimSpace(raw.Name),
		CustomFields: make(map[string]string, len(raw.CustomFields)),
	}
	if raw.AssignedTo != nil {
		asset.AssignedToID = raw.AssignedTo.ID
		asset.AssignedToType = strings.ToLower(strings.TrimSpace(raw.AssignedTo.Type))
		asset.AssignedEmail = strings.ToLower(strings.TrimSpace(raw.AssignedTo.Email))
	}
	if raw.StatusLabel != nil {
		asset.StatusID = raw.StatusLabel.ID
		asset.Status = strings.TrimSpace(raw.StatusLabel.Name)
		asset.Archived = strings.EqualFold(raw.StatusLabel.StatusMeta, "archived") || strings.EqualFold(raw.StatusLabel.Type, "archived")
	}
	if raw.Manufacturer != nil {
		asset.Manufacturer = strings.TrimSpace(raw.Manufacturer.Name)
	}
	for name, field := range raw.CustomFields {
		value := fmt.Sprint(field.Value)
		if field.Value == nil {
			value = ""
		}
		asset.CustomFields[name] = strings.TrimSpace(value)
	}
	for name, value := range asset.CustomFields {
		if strings.EqualFold(name, managedByLabel) {
			asset.ManagedBy = value
		}
	}
	return asset, nil
}
