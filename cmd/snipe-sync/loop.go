package main

import (
	"context"
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
	attributes := []any{"duration", time.Since(started)}
	if result.Plan != nil {
		logEvaluations(logger, *result.Plan)
		userCounts := result.Plan.UserCounts()
		assetCounts := result.Plan.AssetCounts()
		attributes = append(attributes,
			"users", len(result.Plan.Users), "users_create", userCounts[planner.UserCreate],
			"users_update", userCounts[planner.UserUpdate], "users_disable", userCounts[planner.UserDisable],
			"assets", len(result.Plan.Assets), "assets_change", assetCounts[planner.AssetChange],
			"assets_skipped", assetCounts[planner.AssetSkipped],
		)
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

func logEvaluations(logger *slog.Logger, plan planner.Plan) {
	for _, user := range plan.Users {
		if user.Action == planner.UserNoop {
			logger.Debug("user evaluated", "email", user.Email, "action", user.Action)
		}
	}
	for _, asset := range plan.Assets {
		if asset.Result != planner.AssetChange {
			logger.Debug("asset evaluated", "source", asset.Source, "serial", asset.SerialNumber, "result", asset.Result, "decision", assetResult(asset))
		}
	}
}
