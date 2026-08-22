package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

type reconciler interface {
	Reconcile(context.Context, bool) (app.Result, error)
}

func runLoop(ctx context.Context, interval time.Duration, service reconciler, logger *slog.Logger) {
	for {
		_ = runCycle(ctx, service, logger)
		if ctx.Err() != nil {
			return
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func runCycle(ctx context.Context, service reconciler, logger *slog.Logger) error {
	started := time.Now()
	result, err := service.Reconcile(ctx, true)
	if ctx.Err() != nil {
		return err
	}
	if result.Apply != nil {
		logReconciliation(logger, result, err)
	}
	userCounts := result.Plan.UserCounts()
	assetCounts := result.Plan.AssetCounts()
	attributes := []any{
		"users", len(result.Plan.Users),
		"users_create", userCounts[planner.UserCreate],
		"users_update", userCounts[planner.UserUpdate],
		"users_disable", userCounts[planner.UserDisable],
		"assets", len(result.Plan.Assets),
		"assets_change", assetCounts[planner.AssetChange],
		"assets_skipped", assetCounts[planner.AssetSkipped],
		"duration", time.Since(started),
	}
	if result.Apply != nil {
		attributes = append(attributes,
			"users_applied", result.Apply.UsersApplied,
			"assets_applied", result.Apply.AssetsApplied,
			"errors", len(result.Apply.Failures),
		)
	}
	if err != nil {
		logger.ErrorContext(ctx, "reconciliation failed", append(attributes, "error", err)...)
	} else {
		logger.DebugContext(ctx, "reconciliation complete", attributes...)
	}
	return err
}

func logReconciliation(logger *slog.Logger, result app.Result, reconcileErr error) {
	for _, warning := range result.Plan.Warnings {
		logger.Warn("reconciliation warning", "warning", warning)
	}
	failures := make(map[string]string, len(result.Apply.Failures))
	for _, failure := range result.Apply.Failures {
		failures[failure.Kind+"\x00"+failure.Identifier] = failure.Error
	}
	interrupted := errors.Is(reconcileErr, context.Canceled) || errors.Is(reconcileErr, context.DeadlineExceeded)
	for _, user := range result.Plan.Users {
		attributes := []any{"email", user.Email, "action", user.Action}
		failure := failures["user\x00"+user.Email]
		switch {
		case failure != "":
			logger.Warn("user reconciliation failed", append(attributes, "error", failure)...)
		case user.Action == planner.UserNoop:
			logger.Debug("user evaluated", attributes...)
		case interrupted:
			logger.Debug("user outcome unavailable", attributes...)
		default:
			logger.Info("user reconciled", attributes...)
		}
	}
	for _, asset := range result.Plan.Assets {
		attributes := []any{
			"source", asset.Source,
			"serial", asset.SerialNumber,
			"result", asset.Result,
			"decision", assetResult(asset),
		}
		failure := failures["asset\x00"+asset.SerialNumber]
		switch {
		case failure != "":
			logger.Warn("asset reconciliation failed", append(attributes, "error", failure)...)
		case asset.Result == planner.AssetChange:
			if interrupted {
				logger.Debug("asset outcome unavailable", attributes...)
			} else {
				logger.Info("asset reconciled", attributes...)
			}
		default:
			logger.Debug("asset evaluated", attributes...)
		}
	}
}
