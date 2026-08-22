package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/config"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func newRootCommand() *cobra.Command {
	var configPaths []string
	command := &cobra.Command{
		Use:           "snipe-sync",
		Short:         "Reconcile Entra and managed-device inventory into Snipe-IT",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.PersistentFlags().StringArrayVar(
		&configPaths,
		"config",
		defaultConfigPaths(),
		"path to a YAML configuration file; may be repeated in overlay order",
	)
	command.AddCommand(
		newValidateCommand(&configPaths),
		newPlanCommand(&configPaths),
		newRunCommand(&configPaths),
		newSchemaCommand(),
		newVersionCommand(),
	)
	return command
}

func defaultConfigPaths() []string {
	info, err := os.Stat("config.yaml")
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return []string{"config.yaml"}
}

func newValidateCommand(configPaths *[]string) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration and policy expressions",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, err := config.Load((*configPaths)...); err != nil {
				return fmt.Errorf("validate configuration: %w", err)
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), "configuration valid")
			return err
		},
	}
}

func newPlanCommand(configPaths *[]string) *cobra.Command {
	var output string
	command := &cobra.Command{
		Use:   "plan",
		Short: "Fetch complete snapshots and print a read-only reconciliation plan",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if output != "human" && output != "json" {
				return fmt.Errorf("output must be human or json")
			}
			service, err := loadService(*configPaths)
			if err != nil {
				return err
			}
			result, err := service.Reconcile(command.Context(), false)
			if err != nil {
				return err
			}
			return writePlan(command.OutOrStdout(), output, result.Plan)
		},
	}
	command.Flags().StringVar(&output, "output", "human", "plan output format: human or json")
	return command
}

func newRunCommand(configPaths *[]string) *cobra.Command {
	var once bool
	var logLevel string
	command := &cobra.Command{
		Use:   "run",
		Short: "Reconcile immediately, then continue at the configured interval",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			level, err := parseLogLevel(logLevel)
			if err != nil {
				return err
			}
			cfg, service, err := loadConfiguredService(*configPaths)
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewJSONHandler(command.ErrOrStderr(), &slog.HandlerOptions{Level: level}))
			logger.Info("service started", "version", version, "config", *configPaths, "once", once)
			if once {
				return runCycle(command.Context(), service, logger)
			}
			runLoop(command.Context(), cfg.Reconcile.PollInterval.Duration, service, logger)
			return nil
		},
	}
	command.Flags().BoolVar(&once, "once", false, "run one reconciliation cycle and exit")
	command.Flags().StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, or error")
	return command
}

func loadService(configPaths []string) (*app.Service, error) {
	_, service, err := loadConfiguredService(configPaths)
	return service, err
}

func loadConfiguredService(configPaths []string) (*config.Config, *app.Service, error) {
	cfg, err := config.Load(configPaths...)
	if err != nil {
		return nil, nil, fmt.Errorf("load configuration: %w", err)
	}
	service, err := app.Build(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("start service: %w", err)
	}
	return cfg, service, nil
}

func newSchemaCommand() *cobra.Command {
	var outputPath string
	command := &cobra.Command{
		Use:   "schema",
		Short: "Generate the JSON Schema used by YAML editors",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			document, err := config.JSONSchemaDocument()
			if err != nil {
				return fmt.Errorf("generate config schema: %w", err)
			}
			if outputPath == "-" {
				_, err = command.OutOrStdout().Write(document)
				return err
			}
			if err := os.WriteFile(outputPath, document, 0o644); err != nil {
				return fmt.Errorf("write config schema: %w", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&outputPath, "output", "-", "schema output path, or - for stdout")
	return command
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(command.OutOrStdout(), "snipe-sync %s\ncommit: %s\nbuilt: %s\n", version, commit, date)
			return err
		},
	}
}

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
		logger.InfoContext(ctx, "reconciliation complete", attributes...)
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

func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(value) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("log level must be debug, info, warn, or error")
	}
}
