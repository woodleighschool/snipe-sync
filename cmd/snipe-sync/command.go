package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/config"
)

type cli struct {
	configPaths []string
	output      *commandOutput
}

func newRootCommand() (*cobra.Command, *commandOutput) {
	c := &cli{output: &commandOutput{}}
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
	output := c.output
	command.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return output.start(cmd)
	}
	command.SetVersionTemplate(fmt.Sprintf("snipe-sync %s (commit %s, built %s)\n", version, commit, date))
	command.PersistentFlags().Bool("no-progress", false, "Disable terminal progress")
	command.PersistentFlags().StringArrayVar(
		&c.configPaths,
		"config",
		defaultConfigPaths(),
		"path to a YAML configuration file; may be repeated in overlay order",
	)
	command.AddCommand(
		c.validateCommand(),
		c.reconciliationCommand(false),
		c.reconciliationCommand(true),
		c.runCommand(),
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

func (c *cli) validateCommand() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration and policy expressions",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if _, err := c.loadConfig(command); err != nil {
				return fmt.Errorf("validate configuration: %w", err)
			}
			if jsonOutput {
				return json.NewEncoder(command.OutOrStdout()).Encode(struct {
					Valid bool `json:"valid"`
				}{true})
			}
			_, err := fmt.Fprintln(command.OutOrStdout(), newTextStyle(command.OutOrStdout()).paint("✓ Configuration is valid.", color.FgHiGreen))
			return err
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write the validation result as JSON")
	return command
}

func (c *cli) reconciliationCommand(apply bool) *cobra.Command {
	var includeUnchanged bool
	var jsonOutput bool
	name, description := "plan", "Fetch complete snapshots and print a read-only reconciliation plan"
	if apply {
		name, description = "apply", "Apply one reconciliation cycle and print its result"
	}
	command := &cobra.Command{
		Use:   name,
		Short: description,
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cfg, err := c.loadConfig(command)
			if err != nil {
				return err
			}
			service, err := app.Build(cfg, c.output.logger)
			if err != nil {
				return fmt.Errorf("start service: %w", err)
			}
			result, reconcileErr := service.Reconcile(command.Context(), apply)
			return reportResult(command.OutOrStdout(), jsonOutput, includeUnchanged, result, reconcileErr)
		},
	}
	command.Flags().BoolVar(&includeUnchanged, "all", false, "Include unchanged users and assets in the human report")
	command.Flags().BoolVar(&jsonOutput, "json", false, "Write the final report as JSON")
	return command
}

func (c *cli) runCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "run",
		Short: "Reconcile immediately, then continue at the configured interval",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			cfg, err := c.loadConfig(command)
			if err != nil {
				return err
			}
			service, err := app.Build(cfg, c.output.logger)
			if err != nil {
				return fmt.Errorf("start service: %w", err)
			}
			c.output.logger.Info("Service started", "version", version)
			runLoop(command.Context(), cfg.Reconcile.PollInterval.Duration, service, c.output.logger)
			return nil
		},
	}
	command.Flags().StringVar(&c.output.level, "log-level", "info", "Log level: debug, info, warn or error")
	return command
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
			_, err := fmt.Fprintf(command.OutOrStdout(), "snipe-sync %s (commit %s, built %s)\n", version, commit, date)
			return err
		},
	}
}

func (c *cli) loadConfig(command *cobra.Command) (*config.Config, error) {
	cfg, err := config.Load(c.configPaths...)
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}
	if c.output.daemon && !command.Flags().Changed("log-level") {
		c.output.threshold.Set(cfg.ParsedLevel)
	}
	c.output.logger.DebugContext(command.Context(), "Loading application")
	return cfg, nil
}
