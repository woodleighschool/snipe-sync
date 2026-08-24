package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func writePlan(writer io.Writer, output string, includeUnchanged bool, plan planner.Plan) error {
	if output == "json" {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(plan); err != nil {
			return fmt.Errorf("write JSON plan: %w", err)
		}
		return nil
	}
	return writeHumanPlan(writer, includeUnchanged, plan)
}

func writeHumanPlan(writer io.Writer, includeUnchanged bool, plan planner.Plan) error {
	for _, warning := range plan.Warnings {
		if _, err := fmt.Fprintf(writer, "Warning: %s\n", warning); err != nil {
			return fmt.Errorf("write warning: %w", err)
		}
	}
	userCounts := plan.UserCounts()
	if _, err := fmt.Fprintf(
		writer,
		"Users: %d create, %d update, %d disable, %d unchanged\n\nDevices\n",
		userCounts[planner.UserCreate],
		userCounts[planner.UserUpdate],
		userCounts[planner.UserDisable],
		userCounts[planner.UserNoop],
	); err != nil {
		return fmt.Errorf("write user summary: %w", err)
	}
	table := tabwriter.NewWriter(writer, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(table, "SOURCE\tSERIAL\tNAME\tASSIGNMENT\tMANAGED BY\tSTATUS\tRESULT"); err != nil {
		return fmt.Errorf("write device header: %w", err)
	}
	for _, asset := range plan.Assets {
		if !includeUnchanged && asset.Result == planner.AssetUnchanged && asset.Note == "" {
			continue
		}
		if _, err := fmt.Fprintf(
			table,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			asset.Source,
			asset.SerialNumber,
			formatChange(asset.CurrentName, asset.DesiredName),
			formatChange(asset.CurrentAssignment, asset.DesiredAssignment),
			formatChange(asset.CurrentManagedBy, asset.DesiredManagedBy),
			formatChange(asset.CurrentStatus, asset.DesiredStatus),
			assetResult(asset),
		); err != nil {
			return fmt.Errorf("write device plan: %w", err)
		}
	}
	if err := table.Flush(); err != nil {
		return fmt.Errorf("flush device plan: %w", err)
	}
	assetCounts := plan.AssetCounts()
	_, err := fmt.Fprintf(
		writer,
		"\nDevice summary: %d total, %d change, %d unchanged, %d skipped\n",
		len(plan.Assets),
		assetCounts[planner.AssetChange],
		assetCounts[planner.AssetUnchanged],
		assetCounts[planner.AssetSkipped],
	)
	return err
}

func formatChange(current, desired string) string {
	current = displayValue(current)
	desired = displayValue(desired)
	if current == desired {
		return current
	}
	if current == "—" {
		return desired
	}
	return current + " → " + desired
}

func displayValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "—"
	}
	return value
}

func assetResult(asset planner.AssetPlan) string {
	if asset.Result == planner.AssetSkipped {
		return "Skip: " + asset.SkipReason
	}
	actions := make([]string, 0, 4)
	if asset.Patch.Name != nil {
		actions = append(actions, "name")
	}
	if asset.Patch.ManagedBy != nil {
		actions = append(actions, "managed by")
	}
	if asset.Patch.StatusID != nil {
		actions = append(actions, "status")
	}
	switch {
	case asset.Checkin && asset.CheckoutUser != "":
		actions = append(actions, "reassign")
	case asset.Checkin:
		actions = append(actions, "check in")
	case asset.CheckoutUser != "":
		actions = append(actions, "checkout")
	}
	result := "No change"
	if len(actions) != 0 {
		result = strings.Join(actions, ", ")
	}
	if asset.Note != "" {
		result += "; " + asset.Note
	}
	return result
}
