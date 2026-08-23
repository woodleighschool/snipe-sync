package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/config"
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
	command := &cobra.Command{
		Use:   "run",
		Short: "Reconcile immediately, then continue at the configured interval",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cfg, service, err := loadConfiguredService(*configPaths)
			if err != nil {
				return err
			}
			logger := slog.New(slog.NewJSONHandler(command.ErrOrStderr(), &slog.HandlerOptions{Level: cfg.ParsedLevel}))
			logger.Info("service started", "version", version, "config", *configPaths, "once", once)
			if once {
				return runCycle(command.Context(), service, logger)
			}
			runLoop(command.Context(), cfg.Reconcile.PollInterval.Duration, service, logger)
			return nil
		},
	}
	command.Flags().BoolVar(&once, "once", false, "run one reconciliation cycle and exit")
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
