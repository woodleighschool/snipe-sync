package planner

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/expression"
)

var mdmNameSuffix = regexp.MustCompile(`\s*\(\d+\)$`)

type sourcePolicy struct {
	priority  int
	managedBy string
}

type locationPolicy struct {
	name    string
	id      int64
	program expression.Program
}

// Planner produces deterministic plans from complete provider-neutral snapshots.
type Planner struct {
	domains              map[string]struct{}
	sources              map[string]sourcePolicy
	orderedSources       []string
	userInclude          expression.Program
	locations            []locationPolicy
	disabledDepartmentID int64
	departments          map[string][]int64
	manufacturers        map[string]struct{}
	writableStatuses     map[int64]struct{}
	promoteStatuses      map[int64]int64
	promoteTargetName    string
	assetSkips           []config.AssetSkipProgram
	absentAssets         bool
}

// New validates target metadata references and creates a pure planner.
func New(cfg *config.Config, metadata Metadata) (*Planner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	disabledDepartmentID, err := resolveUnique(metadata.Departments, cfg.Users.Absent.Department)
	if err != nil {
		return nil, fmt.Errorf("users.absent.department: %w", err)
	}
	planner := &Planner{
		domains:              make(map[string]struct{}, len(cfg.Identity.Domains)),
		sources:              make(map[string]sourcePolicy, len(cfg.Devices)),
		userInclude:          cfg.Programs.UserInclude,
		disabledDepartmentID: disabledDepartmentID,
		departments:          metadata.Departments,
		manufacturers:        make(map[string]struct{}, len(cfg.Assets.Manufacturers)),
		writableStatuses:     make(map[int64]struct{}, len(cfg.Assets.Statuses.Writable)),
		promoteStatuses:      make(map[int64]int64, len(cfg.Assets.Statuses.Promote.From)),
		promoteTargetName:    cfg.Assets.Statuses.Promote.To,
		assetSkips:           cfg.Programs.AssetSkips,
		absentAssets:         cfg.Assets.Absent.Enabled,
	}
	for _, value := range cfg.Identity.Domains {
		planner.domains[strings.ToLower(value)] = struct{}{}
	}
	for _, source := range cfg.Devices {
		planner.sources[source.Name] = sourcePolicy{priority: source.Priority, managedBy: source.ManagedBy}
		planner.orderedSources = append(planner.orderedSources, source.Name)
	}
	sort.Slice(planner.orderedSources, func(left, right int) bool {
		return planner.sources[planner.orderedSources[left]].priority > planner.sources[planner.orderedSources[right]].priority
	})
	for index, locationCase := range cfg.Users.Location.Cases {
		locationID, resolveErr := resolveUnique(metadata.Locations, locationCase.Value)
		if resolveErr != nil {
			return nil, fmt.Errorf("users.location.cases[%d].value: %w", index, resolveErr)
		}
		planner.locations = append(planner.locations, locationPolicy{
			name: locationCase.Value, id: locationID, program: cfg.Programs.Locations[index],
		})
	}
	for _, manufacturer := range cfg.Assets.Manufacturers {
		key := strings.ToLower(strings.TrimSpace(manufacturer))
		if metadata.Manufacturers[key] != 1 {
			return nil, fmt.Errorf("assets.manufacturers: Snipe value %q was not found or is ambiguous", manufacturer)
		}
		planner.manufacturers[key] = struct{}{}
	}
	for _, status := range cfg.Assets.Statuses.Writable {
		statusID, resolveErr := resolveUnique(metadata.Statuses, status)
		if resolveErr != nil {
			return nil, fmt.Errorf("assets.statuses.writable: %w", resolveErr)
		}
		planner.writableStatuses[statusID] = struct{}{}
	}
	if len(cfg.Assets.Statuses.Promote.From) != 0 {
		targetID, resolveErr := resolveUnique(metadata.Statuses, cfg.Assets.Statuses.Promote.To)
		if resolveErr != nil {
			return nil, fmt.Errorf("assets.statuses.promote.to: %w", resolveErr)
		}
		for _, status := range cfg.Assets.Statuses.Promote.From {
			statusID, fromErr := resolveUnique(metadata.Statuses, status)
			if fromErr != nil {
				return nil, fmt.Errorf("assets.statuses.promote.from: %w", fromErr)
			}
			planner.promoteStatuses[statusID] = targetID
		}
	}
	return planner, nil
}

// Plan produces a complete immutable reconciliation plan without performing writes.
func (p *Planner) Plan(input Input) (Plan, error) {
	for _, source := range p.orderedSources {
		if _, complete := input.DevicesBySource[source]; !complete {
			return Plan{}, fmt.Errorf("device source %q snapshot is incomplete", source)
		}
	}
	users, userWarnings, err := p.planUsers(input.Users, input.TargetUsers)
	if err != nil {
		return Plan{}, err
	}
	assets, err := p.planAssets(input.DevicesBySource, input.Assets, input.TargetUsers, users)
	if err != nil {
		return Plan{}, err
	}
	warnings := append(append([]string(nil), input.Warnings...), userWarnings...)
	sort.Strings(warnings)
	warnings = slices.Compact(warnings)
	return Plan{Warnings: warnings, Users: users, Assets: assets}, nil
}

func (p *Planner) planUsers(users []domain.User, targetUsers map[string]domain.TargetUser) ([]UserPlan, []string, error) {
	eligible := make([]domain.User, 0, len(users))
	for _, user := range users {
		user.UserPrincipalName = normalizeEmail(user.UserPrincipalName)
		if !user.Present || !p.internalEmail(user.UserPrincipalName) || strings.Contains(user.UserPrincipalName, "#ext#") || strings.TrimSpace(user.GivenName) == "" {
			continue
		}
		included, err := p.userInclude.Eval(expression.Input{User: &user})
		if err != nil {
			return nil, nil, fmt.Errorf("evaluate user inclusion for %s: %w", user.UserPrincipalName, err)
		}
		if included {
			eligible = append(eligible, user)
		}
	}
	sort.Slice(eligible, func(left, right int) bool {
		return eligible[left].UserPrincipalName < eligible[right].UserPrincipalName
	})
	plans := make([]UserPlan, 0, len(eligible))
	warnings := make([]string, 0)
	present := make(map[string]struct{}, len(eligible))
	for index := range eligible {
		user := &eligible[index]
		present[user.UserPrincipalName] = struct{}{}
		departmentID, departmentKnown := resolveOptional(p.departments, user.Department)
		if user.Department != "" && !departmentKnown {
			warnings = append(warnings, fmt.Sprintf("Snipe department %q for %s was not found or is ambiguous; department preserved", user.Department, user.UserPrincipalName))
		}
		locationID, locationKnown, locationErr := p.location(user)
		if locationErr != nil {
			return nil, nil, locationErr
		}
		target, exists := targetUsers[user.UserPrincipalName]
		if !exists {
			patch := fullUserPatch(*user)
			if departmentKnown {
				patch.DepartmentID = &departmentID
			}
			if locationKnown {
				patch.LocationID = &locationID
			}
			plans = append(plans, UserPlan{Email: user.UserPrincipalName, Action: UserCreate, Patch: patch})
			continue
		}
		patch := updateUserPatch(*user, target)
		if departmentKnown && target.DepartmentID != departmentID {
			patch.DepartmentID = &departmentID
		}
		if locationKnown && target.LocationID != locationID {
			patch.LocationID = &locationID
		}
		action := UserNoop
		if !patch.Empty() {
			action = UserUpdate
		}
		plans = append(plans, UserPlan{Email: user.UserPrincipalName, Action: action, TargetID: target.ID, Patch: patch})
	}
	targetEmails := make([]string, 0, len(targetUsers))
	for email := range targetUsers {
		targetEmails = append(targetEmails, email)
	}
	sort.Strings(targetEmails)
	for _, email := range targetEmails {
		target := targetUsers[email]
		if !p.internalEmail(email) {
			continue
		}
		if _, exists := present[email]; exists || target.DepartmentID == p.disabledDepartmentID {
			continue
		}
		departmentID := p.disabledDepartmentID
		plans = append(plans, UserPlan{
			Email: email, Action: UserDisable, TargetID: target.ID,
			Patch: domain.UserPatch{DepartmentID: &departmentID},
		})
	}
	sort.SliceStable(plans, func(left, right int) bool { return plans[left].Email < plans[right].Email })
	return plans, warnings, nil
}

func (p *Planner) location(user *domain.User) (int64, bool, error) {
	if !user.GroupsComplete {
		return 0, false, nil
	}
	for _, location := range p.locations {
		matched, err := location.program.Eval(expression.Input{User: user})
		if err != nil {
			return 0, false, fmt.Errorf("evaluate location for %s: %w", user.UserPrincipalName, err)
		}
		if matched {
			return location.id, true, nil
		}
	}
	return 0, false, nil
}

func (p *Planner) planAssets(
	devicesBySource map[string][]domain.Device,
	assets map[string]domain.Asset,
	targetUsers map[string]domain.TargetUser,
	userPlans []UserPlan,
) ([]AssetPlan, error) {
	devices := p.selectDevices(devicesBySource)
	plannedCreates := make(map[string]struct{})
	for _, user := range userPlans {
		if user.Action == UserCreate {
			plannedCreates[user.Email] = struct{}{}
		}
	}
	usersByID := make(map[int64]domain.TargetUser, len(targetUsers))
	for _, user := range targetUsers {
		usersByID[user.ID] = user
	}
	plans := make([]AssetPlan, 0, len(devices)+len(assets))
	serials := make([]string, 0, len(devices))
	for serial := range devices {
		serials = append(serials, serial)
	}
	sort.Slice(serials, func(left, right int) bool {
		leftDevice := devices[serials[left]]
		rightDevice := devices[serials[right]]
		leftPriority := p.sources[leftDevice.Source].priority
		rightPriority := p.sources[rightDevice.Source].priority
		if leftPriority != rightPriority {
			return leftPriority > rightPriority
		}
		return serials[left] < serials[right]
	})
	for _, serial := range serials {
		device := devices[serial]
		asset, exists := assets[serial]
		if !exists {
			plans = append(plans, skippedDevice(device, p.sources[device.Source].managedBy, "missing in Snipe"))
			continue
		}
		plan, err := p.planPresentAsset(device, asset, targetUsers, usersByID, plannedCreates)
		if err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	if p.absentAssets {
		absentSerials := make([]string, 0)
		for serial, asset := range assets {
			if _, present := devices[serial]; present || asset.Archived || !p.supportedManufacturer(asset.Manufacturer) {
				continue
			}
			absentSerials = append(absentSerials, serial)
		}
		sort.Strings(absentSerials)
		for _, serial := range absentSerials {
			plans = append(plans, planAbsentAsset(assets[serial], usersByID))
		}
	}
	return plans, nil
}

func (p *Planner) selectDevices(devicesBySource map[string][]domain.Device) map[string]domain.Device {
	selected := make(map[string]domain.Device)
	for _, source := range p.orderedSources {
		unique := make(map[string]domain.Device)
		for _, raw := range devicesBySource[source] {
			device := normalizeDevice(raw, source)
			if device.SerialNumber == "" {
				continue
			}
			previous, exists := unique[device.SerialNumber]
			if !exists || newerDevice(device, previous) {
				unique[device.SerialNumber] = device
			}
		}
		for serial, device := range unique {
			if _, claimed := selected[serial]; !claimed {
				selected[serial] = device
			}
		}
	}
	return selected
}

func (p *Planner) planPresentAsset(
	device domain.Device,
	asset domain.Asset,
	targetUsers map[string]domain.TargetUser,
	usersByID map[int64]domain.TargetUser,
	plannedCreates map[string]struct{},
) (AssetPlan, error) {
	if asset.Archived {
		return skippedAsset(device, asset, usersByID, "archived"), nil
	}
	if !p.supportedManufacturer(asset.Manufacturer) {
		return skippedAsset(device, asset, usersByID, "unsupported manufacturer "+asset.Manufacturer), nil
	}
	plan := baseAssetPlan(device, asset, usersByID)
	if _, writable := p.writableStatuses[asset.StatusID]; !writable {
		targetStatusID, promote := p.promoteStatuses[asset.StatusID]
		if !promote {
			reason := strings.TrimSpace(asset.Status) + " blocks sync"
			if asset.StatusID == 0 {
				reason = "missing status"
			}
			plan.Result = AssetSkipped
			plan.SkipReason = reason
			return plan, nil
		}
		plan.Patch.StatusID = &targetStatusID
		plan.DesiredStatus = p.promoteTargetName
	}
	if device.Name != "" && device.Name != asset.Name {
		name := device.Name
		plan.Patch.Name = &name
		plan.DesiredName = name
	}
	desiredManagedBy := p.sources[device.Source].managedBy
	if desiredManagedBy != asset.ManagedBy {
		plan.Patch.ManagedBy = &desiredManagedBy
		plan.DesiredManagedBy = desiredManagedBy
	}

	assignmentNote := ""
	desiredUser := normalizeEmail(device.PrimaryUserPrincipalName)
	if desiredUser != "" {
		target, targetExists := targetUsers[desiredUser]
		_, createPlanned := plannedCreates[desiredUser]
		if !targetExists && !createPlanned {
			plan.Note = fmt.Sprintf("primary user %s is missing in Snipe; checkout preserved", desiredUser)
		} else if !assignedToUser(asset, desiredUser, target) {
			plan.Checkin = asset.AssignedToID != 0
			plan.CheckoutUser = desiredUser
			plan.CheckoutAt = device.EnrolledAt
			plan.DesiredAssignment = desiredUser
			if createPlanned {
				assignmentNote = "checkout follows planned user create"
			}
		}
	} else if asset.AssignedToID != 0 {
		switch device.Name {
		case "":
			plan.Note = "blank MDM name and primary user; checkout preserved"
		default:
			plan.Checkin = true
			plan.DesiredAssignment = ""
		}
	}
	skippedFields, err := p.applyAssetFieldSkips(&plan, device, asset)
	if err != nil {
		return AssetPlan{}, err
	}
	if assignmentNote != "" && (plan.Checkin || plan.CheckoutUser != "") {
		plan.Note = appendPlanNote(plan.Note, assignmentNote)
	}
	if len(skippedFields) != 0 {
		names := make([]string, 0, len(skippedFields))
		for _, field := range skippedFields {
			names = append(names, string(field))
		}
		plan.Note = appendPlanNote(plan.Note, strings.Join(names, ", ")+" skipped by policy")
	}
	if plan.HasChanges() {
		plan.Result = AssetChange
	}
	return plan, nil
}

func (p *Planner) applyAssetFieldSkips(plan *AssetPlan, device domain.Device, asset domain.Asset) ([]config.AssetField, error) {
	matches := make(map[config.AssetField]struct{})
	for index, rule := range p.assetSkips {
		matched, err := rule.When.Eval(expression.Input{Device: &device, Asset: &asset})
		if err != nil {
			return nil, fmt.Errorf("evaluate assets.skip[%d] for %s: %w", index, device.SerialNumber, err)
		}
		if matched {
			for _, field := range rule.Fields {
				matches[field] = struct{}{}
			}
		}
	}

	skipped := make([]config.AssetField, 0, len(matches))
	if _, ok := matches[config.AssetFieldName]; ok && plan.Patch.Name != nil {
		plan.Patch.Name = nil
		plan.DesiredName = plan.CurrentName
		skipped = append(skipped, config.AssetFieldName)
	}
	if _, ok := matches[config.AssetFieldManagedBy]; ok && plan.Patch.ManagedBy != nil {
		plan.Patch.ManagedBy = nil
		plan.DesiredManagedBy = plan.CurrentManagedBy
		skipped = append(skipped, config.AssetFieldManagedBy)
	}
	if _, ok := matches[config.AssetFieldAssignment]; ok && (plan.Checkin || plan.CheckoutUser != "") {
		plan.Checkin = false
		plan.CheckoutUser = ""
		plan.CheckoutAt = time.Time{}
		plan.DesiredAssignment = plan.CurrentAssignment
		skipped = append(skipped, config.AssetFieldAssignment)
	}
	return skipped, nil
}

func appendPlanNote(current, note string) string {
	if current == "" {
		return note
	}
	return current + "; " + note
}

func (p *Planner) internalEmail(email string) bool {
	separator := strings.LastIndexByte(email, '@')
	if separator < 0 {
		return false
	}
	_, ok := p.domains[email[separator+1:]]
	return ok
}

func (p *Planner) supportedManufacturer(name string) bool {
	_, ok := p.manufacturers[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

func fullUserPatch(user domain.User) domain.UserPatch {
	givenName := strings.TrimSpace(user.GivenName)
	surname := strings.TrimSpace(user.Surname)
	username := strings.TrimSpace(user.MailNickname)
	email := user.UserPrincipalName
	patch := domain.UserPatch{GivenName: &givenName, Surname: &surname, Username: &username, Email: &email}
	if !user.CreatedAt.IsZero() {
		startDate := user.CreatedAt.Format(time.DateOnly)
		patch.StartDate = &startDate
	}
	return patch
}

func updateUserPatch(user domain.User, target domain.TargetUser) domain.UserPatch {
	patch := domain.UserPatch{}
	if value := strings.TrimSpace(user.GivenName); target.GivenName != value {
		patch.GivenName = &value
	}
	if value := strings.TrimSpace(user.Surname); target.Surname != value {
		patch.Surname = &value
	}
	if value := strings.TrimSpace(user.MailNickname); target.Username != value {
		patch.Username = &value
	}
	if !user.CreatedAt.IsZero() {
		value := user.CreatedAt.Format(time.DateOnly)
		if target.StartDate != value {
			patch.StartDate = &value
		}
	}
	return patch
}

func resolveUnique(values map[string][]int64, name string) (int64, error) {
	matches := values[strings.ToLower(strings.TrimSpace(name))]
	if len(matches) == 0 {
		return 0, fmt.Errorf("snipe value %q was not found", name)
	}
	if len(matches) > 1 {
		return 0, fmt.Errorf("snipe value %q is ambiguous", name)
	}
	return matches[0], nil
}

func resolveOptional(values map[string][]int64, name string) (int64, bool) {
	if strings.TrimSpace(name) == "" {
		return 0, false
	}
	matches := values[strings.ToLower(strings.TrimSpace(name))]
	if len(matches) != 1 {
		return 0, false
	}
	return matches[0], true
}

func normalizeDevice(device domain.Device, source string) domain.Device {
	device.Source = source
	device.SerialNumber = strings.ToUpper(strings.TrimSpace(device.SerialNumber))
	device.Name = mdmNameSuffix.ReplaceAllString(strings.TrimSpace(device.Name), "")
	device.PrimaryUserPrincipalName = normalizeEmail(device.PrimaryUserPrincipalName)
	return device
}

func newerDevice(candidate, current domain.Device) bool {
	if !candidate.LastContactAt.Equal(current.LastContactAt) {
		return candidate.LastContactAt.After(current.LastContactAt)
	}
	return candidate.Namespace+"\x00"+candidate.ID < current.Namespace+"\x00"+current.ID
}

func normalizeEmail(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func assignedToUser(asset domain.Asset, email string, target domain.TargetUser) bool {
	if asset.AssignedToType != "user" {
		return false
	}
	if normalizeEmail(asset.AssignedEmail) == email {
		return true
	}
	return target.ID != 0 && asset.AssignedToID == target.ID
}

func baseAssetPlan(device domain.Device, asset domain.Asset, usersByID map[int64]domain.TargetUser) AssetPlan {
	assignment := assignmentLabel(asset, usersByID)
	return AssetPlan{
		Source: device.Source, Namespace: device.Namespace, DeviceID: device.ID,
		SerialNumber: asset.SerialNumber, AssetID: asset.ID,
		CurrentName: asset.Name, DesiredName: asset.Name,
		CurrentAssignment: assignment, DesiredAssignment: assignment,
		CurrentManagedBy: asset.ManagedBy, DesiredManagedBy: asset.ManagedBy,
		CurrentStatus: asset.Status, DesiredStatus: asset.Status,
		Result: AssetUnchanged,
	}
}

func skippedDevice(device domain.Device, managedBy, reason string) AssetPlan {
	return AssetPlan{
		Source: device.Source, Namespace: device.Namespace, DeviceID: device.ID,
		SerialNumber: device.SerialNumber, DesiredName: device.Name,
		DesiredAssignment: device.PrimaryUserPrincipalName,
		DesiredManagedBy:  managedBy, Result: AssetSkipped, SkipReason: reason,
	}
}

func skippedAsset(device domain.Device, asset domain.Asset, usersByID map[int64]domain.TargetUser, reason string) AssetPlan {
	plan := baseAssetPlan(device, asset, usersByID)
	plan.Result = AssetSkipped
	plan.SkipReason = reason
	return plan
}

func planAbsentAsset(asset domain.Asset, usersByID map[int64]domain.TargetUser) AssetPlan {
	assignment := assignmentLabel(asset, usersByID)
	plan := AssetPlan{
		Source: "absent", SerialNumber: asset.SerialNumber, AssetID: asset.ID,
		CurrentName: asset.Name, CurrentAssignment: assignment,
		CurrentManagedBy: asset.ManagedBy, CurrentStatus: asset.Status, DesiredStatus: asset.Status,
		Result: AssetUnchanged,
	}
	if asset.Name != "" {
		value := ""
		plan.Patch.Name = &value
	}
	if asset.ManagedBy != "" {
		value := ""
		plan.Patch.ManagedBy = &value
	}
	if asset.AssignedToID != 0 {
		plan.Checkin = true
	}
	if plan.HasChanges() {
		plan.Result = AssetChange
	}
	return plan
}

func assignmentLabel(asset domain.Asset, usersByID map[int64]domain.TargetUser) string {
	if asset.AssignedToID == 0 {
		return ""
	}
	if asset.AssignedToType == "user" {
		if email := normalizeEmail(asset.AssignedEmail); email != "" {
			return email
		}
		if user, ok := usersByID[asset.AssignedToID]; ok {
			return user.Email
		}
		return fmt.Sprintf("user:%d", asset.AssignedToID)
	}
	return fmt.Sprintf("%s:%d", asset.AssignedToType, asset.AssignedToID)
}
