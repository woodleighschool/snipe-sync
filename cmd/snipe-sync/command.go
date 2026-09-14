package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/config"
)

func newRootCommand() (*cobra.Command, *commandOutput) {
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
	output := newCommandOutput(command)
	command.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return output.start(cmd)
	}
	command.PersistentFlags().StringArrayVar(
		&configPaths,
		"config",
		defaultConfigPaths(),
		"path to a YAML configuration file; may be repeated in overlay order",
	)
	command.AddCommand(
		newValidateCommand(&configPaths, output),
		newReconciliationCommand(&configPaths, output, false),
		newReconciliationCommand(&configPaths, output, true),
		newRunCommand(&configPaths, output),
		newSchemaCommand(),
		newVersionCommand(),
	)
	return command, output
}

func defaultConfigPaths() []string {
	info, err := os.Stat("config.yaml")
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	return []string{"config.yaml"}
}

func newValidateCommand(configPaths *[]string, diagnostics *commandOutput) *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration and policy expressions",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, err := loadConfig(command, *configPaths, diagnostics); err != nil {
				return fmt.Errorf("validate configuration: %w", err)
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), "configuration valid")
			return err
		},
	}
}

func newReconciliationCommand(configPaths *[]string, diagnostics *commandOutput, apply bool) *cobra.Command {
	var includeUnchanged bool
	var output string
	name, description := "plan", "Fetch complete snapshots and print a read-only reconciliation plan"
	if apply {
		name, description = "apply", "Apply one reconciliation cycle and print its result"
	}
	command := &cobra.Command{
		Use:   name,
		Short: description,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if output != "text" && output != "json" {
				return fmt.Errorf("output must be text or json")
			}
			cfg, err := loadConfig(command, *configPaths, diagnostics)
			if err != nil {
				return err
			}
			service, err := app.Build(cfg, diagnostics.logger)
			if err != nil {
				return fmt.Errorf("start service: %w", err)
			}
			result, reconcileErr := service.Reconcile(command.Context(), apply)
			diagnostics.endProgress(reconcileErr)
			return errors.Join(reconcileErr, writeReport(command.OutOrStdout(), output, includeUnchanged, result, reconcileErr))
		},
	}
	command.Flags().BoolVar(&includeUnchanged, "all", false, "Include unchanged devices in human output")
	command.Flags().StringVar(&output, "output", "text", "Report format: text or json")
	return command
}

func newRunCommand(configPaths *[]string, diagnostics *commandOutput) *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Reconcile immediately, then continue at the configured interval",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cfg, err := loadConfig(command, *configPaths, diagnostics)
			if err != nil {
				return err
			}
			service, err := app.Build(cfg, diagnostics.logger)
			if err != nil {
				return fmt.Errorf("start service: %w", err)
			}
			diagnostics.logger.Info("Service started", "version", version)
			runLoop(command.Context(), cfg.Reconcile.PollInterval.Duration, service, diagnostics.logger)
			return nil
		},
	}
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
	command.Flags().StringVar(&outputPath, "output-file", "-", "schema output path, or - for stdout")
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

func loadConfig(command *cobra.Command, paths []string, diagnostics *commandOutput) (*config.Config, error) {
	cfg, err := config.Load(paths...)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	if !diagnostics.explicitLevel {
		diagnostics.threshold.Set(cfg.ParsedLevel)
	}
	diagnostics.logger.DebugContext(command.Context(), "Loading application")
	return cfg, nil
}
