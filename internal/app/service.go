package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

// Service reconciles complete provider snapshots through one deterministic plan.
type Service struct {
	logger   *slog.Logger
	config   *config.Config
	identity IdentitySource
	sources  []DeviceSource
	target   Target
	timezone *time.Location
}

// New creates a reconciliation service.
func New(cfg *config.Config, identity IdentitySource, sources []DeviceSource, target Target, logger *slog.Logger) (*Service, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if identity == nil || target == nil {
		return nil, fmt.Errorf("identity and target are required")
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("at least one device source is required")
	}
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if source == nil || strings.TrimSpace(source.Name()) == "" {
			return nil, fmt.Errorf("device sources must have names")
		}
		if _, exists := seen[source.Name()]; exists {
			return nil, fmt.Errorf("device source %q is duplicated", source.Name())
		}
		seen[source.Name()] = struct{}{}
	}
	timezone, err := time.LoadLocation(cfg.Target.Timezone)
	if err != nil {
		return nil, fmt.Errorf("load target timezone: %w", err)
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Service{
		logger: logger,
		config: cfg, identity: identity, sources: append([]DeviceSource(nil), sources...),
		target: target, timezone: timezone,
	}, nil
}

// Reconcile reads every source, creates one plan, and optionally applies that exact plan.
func (s *Service) Reconcile(ctx context.Context, apply bool) (Result, error) {
	plan, snapshot, err := s.plan(ctx)
	if err != nil {
		return Result{}, err
	}
	for _, warning := range plan.Warnings {
		s.logger.WarnContext(ctx, "reconciliation warning", "warning", warning)
	}
	result := Result{Plan: &plan}
	if !apply {
		return result, nil
	}
	applyResult, err := s.apply(ctx, plan, snapshot)
	result.Apply = &applyResult
	return result, err
}

func (s *Service) plan(ctx context.Context) (result planner.Plan, snapshot *TargetSnapshot, runErr error) {
	done := s.stage(ctx, "Fetching provider snapshots")
	defer func() { done(runErr) }()
	type sourceResult struct {
		name    string
		devices []domain.Device
	}
	var (
		users          []domain.User
		warnings       []string
		targetSnapshot *TargetSnapshot
	)
	sourceResults := make([]sourceResult, len(s.sources))
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		var err error
		s.logger.DebugContext(groupCtx, "Fetching directory users")
		users, warnings, err = s.identity.ListUsers(groupCtx)
		if err != nil {
			return fmt.Errorf("list identity users: %w", err)
		}
		return nil
	})
	group.Go(func() error {
		var err error
		s.logger.DebugContext(groupCtx, "Fetching Snipe inventory")
		targetSnapshot, err = s.target.Snapshot(groupCtx, s.config.Assets.ManagedByField)
		if err != nil {
			return fmt.Errorf("read Snipe snapshot: %w", err)
		}
		return nil
	})
	for index, source := range s.sources {
		group.Go(func() error {
			s.logger.DebugContext(groupCtx, "Fetching devices", "source", source.Name())
			listed, err := source.ListDevices(groupCtx)
			if err != nil {
				return fmt.Errorf("list %s devices: %w", source.Name(), err)
			}
			sourceResults[index] = sourceResult{name: source.Name(), devices: listed}
			return nil
		})
	}
	err := group.Wait()
	done(err)
	if err != nil {
		return planner.Plan{}, nil, err
	}
	devices := make(map[string][]domain.Device, len(sourceResults))
	for _, result := range sourceResults {
		devices[result.name] = result.devices
	}
	done = s.stage(ctx, "Planning users and assets")
	engine, err := planner.New(s.config, planner.Metadata{
		Departments: targetSnapshot.Departments, Locations: targetSnapshot.Locations,
		Statuses: targetSnapshot.Statuses, Manufacturers: targetSnapshot.Manufacturers,
	})
	if err != nil {
		return planner.Plan{}, nil, fmt.Errorf("resolve Snipe metadata: %w", err)
	}
	plan, err := engine.Plan(planner.Input{
		Users: users, DevicesBySource: devices, TargetUsers: targetSnapshot.Users,
		Assets: targetSnapshot.Assets, Warnings: warnings,
	})
	if err != nil {
		return planner.Plan{}, nil, fmt.Errorf("plan reconciliation: %w", err)
	}
	return plan, targetSnapshot, nil
}

func (s *Service) apply(ctx context.Context, plan planner.Plan, snapshot *TargetSnapshot) (result ApplyResult, runErr error) {
	userIDs := make(map[string]int64, len(snapshot.Users))
	for email, user := range snapshot.Users {
		userIDs[email] = user.ID
	}
	total, completed := 0, 0
	for _, user := range plan.Users {
		if user.Action == planner.UserCreate || user.Action == planner.UserUpdate || user.Action == planner.UserDisable {
			total++
		}
	}
	done := s.stage(ctx, "Applying users", "total", total, "unit", "attempts")
	defer func() { done(runErr) }()
	var failures []error
	failureStart := 0
	for _, action := range []planner.UserAction{planner.UserCreate, planner.UserUpdate, planner.UserDisable} {
		for _, user := range plan.Users {
			if user.Action != action {
				continue
			}
			if err := ctx.Err(); err != nil {
				return result, errors.Join(append(failures, err)...)
			}
			var err error
			if action == planner.UserCreate {
				var userID int64
				userID, err = s.target.CreateUser(ctx, user.Patch)
				if err == nil {
					userIDs[user.Email] = userID
				}
			} else {
				err = s.target.PatchUser(ctx, user.TargetID, user.Patch)
			}
			completed++
			s.logger.InfoContext(ctx, "Applying users", "progress", true, "current", completed, "total", total, "unit", "attempts", "progress_final", completed == total)
			if err != nil {
				failures = append(failures, s.recordFailure(ctx, &result, "user", user.Email, err))
				continue
			}
			result.UsersApplied++
			s.logger.InfoContext(ctx, "user reconciled", "email", user.Email, "action", user.Action)
		}
	}
	type assetApplyState struct {
		plan           planner.AssetPlan
		checkoutUserID int64
		assignmentErr  error
		failed         bool
	}
	assets := make([]assetApplyState, 0, len(plan.Assets))
	for _, asset := range plan.Assets {
		if asset.Result != planner.AssetChange {
			continue
		}
		state := assetApplyState{plan: asset}
		if asset.CheckoutUser != "" {
			state.checkoutUserID = userIDs[asset.CheckoutUser]
			if state.checkoutUserID == 0 {
				state.assignmentErr = fmt.Errorf("checkout user %s is unavailable", asset.CheckoutUser)
			}
		}
		assets = append(assets, state)
	}
	done(errors.Join(failures[failureStart:]...))
	failureStart = len(failures)
	done = s.stage(ctx, "Updating assets", "total", len(assets), "unit", "assets")
	for index := range assets {
		state := &assets[index]
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if !state.plan.Patch.Empty() {
			if err := s.target.PatchAsset(ctx, state.plan.AssetID, state.plan.Patch, snapshot.ManagedByColumn); err != nil {
				state.failed = true
				failures = append(failures, s.recordFailure(ctx,
					&result, "asset", state.plan.SerialNumber, fmt.Errorf("patch: %w", err),
				))
			}
		}
		s.logger.InfoContext(ctx, "Updating assets", "progress", true, "current", index+1, "total", len(assets), "unit", "assets", "progress_final", index+1 == len(assets))
		if !state.failed && state.assignmentErr != nil {
			state.failed = true
			failures = append(failures, s.recordFailure(ctx,
				&result, "asset", state.plan.SerialNumber, state.assignmentErr,
			))
		}
		if !state.failed && !state.plan.Checkin && state.plan.CheckoutUser == "" {
			result.AssetsApplied++
			s.logger.InfoContext(ctx, "asset reconciled", "source", state.plan.Source, "serial", state.plan.SerialNumber)
		}
	}
	total, completed = 0, 0
	for _, state := range assets {
		if !state.failed && state.plan.Checkin {
			total++
		}
	}
	done(errors.Join(failures[failureStart:]...))
	failureStart = len(failures)
	done = s.stage(ctx, "Checking in assets", "total", total, "unit", "attempts")
	for index := range assets {
		state := &assets[index]
		if state.failed || !state.plan.Checkin {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if err := s.target.CheckinAsset(ctx, state.plan.AssetID); err != nil {
			state.failed = true
			failures = append(failures, s.recordFailure(ctx,
				&result, "asset", state.plan.SerialNumber, fmt.Errorf("check in: %w", err),
			))
		}
		completed++
		s.logger.InfoContext(ctx, "Checking in assets", "progress", true, "current", completed, "total", total, "unit", "attempts", "progress_final", completed == total)
		if !state.failed && state.plan.CheckoutUser == "" {
			result.AssetsApplied++
			s.logger.InfoContext(ctx, "asset reconciled", "source", state.plan.Source, "serial", state.plan.SerialNumber)
		}
	}
	total, completed = 0, 0
	for _, state := range assets {
		if !state.failed && state.plan.CheckoutUser != "" {
			total++
		}
	}
	done(errors.Join(failures[failureStart:]...))
	failureStart = len(failures)
	done = s.stage(ctx, "Checking out assets", "total", total, "unit", "attempts")
	for index := range assets {
		state := &assets[index]
		if state.failed || state.plan.CheckoutUser == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(failures, err)...)
		}
		if err := s.target.CheckoutAsset(
			ctx, state.plan.AssetID, state.checkoutUserID, state.plan.CheckoutAt, s.timezone,
		); err != nil {
			state.failed = true
			failures = append(failures, s.recordFailure(ctx,
				&result, "asset", state.plan.SerialNumber, fmt.Errorf("check out: %w", err),
			))
		}
		completed++
		s.logger.InfoContext(ctx, "Checking out assets", "progress", true, "current", completed, "total", total, "unit", "attempts", "progress_final", completed == total)
		if !state.failed {
			result.AssetsApplied++
			s.logger.InfoContext(ctx, "asset reconciled", "source", state.plan.Source, "serial", state.plan.SerialNumber)
		}
	}
	done(errors.Join(failures[failureStart:]...))
	return result, errors.Join(failures...)
}

func (s *Service) recordFailure(ctx context.Context, result *ApplyResult, kind, identifier string, err error) error {
	s.logger.WarnContext(ctx, "reconciliation failed", "kind", kind, "identifier", identifier, "error", err)
	result.Failures = append(result.Failures, Failure{Kind: kind, Identifier: identifier, Error: err.Error()})
	return fmt.Errorf("%s %s: %w", kind, identifier, err)
}

func (s *Service) stage(ctx context.Context, message string, attrs ...any) func(error) {
	started := time.Now()
	s.logger.InfoContext(ctx, message, append([]any{"stage", true}, attrs...)...)
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			result := append([]any{"stage_result", true, "elapsed", time.Since(started).Round(time.Millisecond)}, attrs...)
			if err != nil {
				result = append(result, "error", err)
			}
			s.logger.InfoContext(ctx, message, result...)
		})
	}
}
