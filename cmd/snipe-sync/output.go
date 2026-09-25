package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fatih/color"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

type reconciliationReport struct {
	app.Result
	Totals reportTotals `json:"totals"`
	Error  string       `json:"error,omitempty"`
}

type reportTotals struct {
	Users        int                         `json:"users"`
	UserActions  map[planner.UserAction]int  `json:"user_actions"`
	Assets       int                         `json:"assets"`
	AssetResults map[planner.AssetResult]int `json:"asset_results"`
}

type reportFailure struct {
	cause error
}

func (e reportFailure) Error() string {
	return strings.SplitN(e.cause.Error(), "\n", 2)[0]
}

func (e reportFailure) Unwrap() error { return e.cause }

func reportResult(writer io.Writer, jsonOutput, includeUnchanged bool, result app.Result, runErr error) error {
	if err := writeReport(writer, jsonOutput, includeUnchanged, result, runErr); err != nil {
		return errors.Join(runErr, fmt.Errorf("write report: %w", err))
	}
	if runErr != nil && result.Apply != nil && result.Plan != nil {
		return reportFailure{cause: runErr}
	}
	return runErr
}

func writeReport(writer io.Writer, jsonOutput, includeUnchanged bool, result app.Result, runErr error) error {
	if result.Plan == nil {
		return nil
	}
	plan := *result.Plan
	report := reconciliationReport{Result: result, Totals: reportTotals{
		Users: len(plan.Users), UserActions: plan.UserCounts(), Assets: len(plan.Assets), AssetResults: plan.AssetCounts(),
	}}
	plan.Users = make([]planner.UserPlan, 0, len(result.Plan.Users))
	plan.Assets = make([]planner.AssetPlan, 0, len(result.Plan.Assets))
	for _, user := range result.Plan.Users {
		if jsonOutput || includeUnchanged || user.Action != planner.UserNoop {
			plan.Users = append(plan.Users, user)
		}
	}
	for _, asset := range result.Plan.Assets {
		if jsonOutput || includeUnchanged || asset.Result != planner.AssetUnchanged || asset.Note != "" {
			plan.Assets = append(plan.Assets, asset)
		}
	}
	report.Plan = &plan
	if runErr != nil {
		report.Error = runErr.Error()
	}
	if jsonOutput {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(report)
	}
	style := newTextStyle(writer)
	var text strings.Builder
	label := "Plan"
	if report.Apply != nil {
		label = "Apply"
	}
	fmt.Fprintln(&text, style.paint(label, color.Bold))
	for _, warning := range plan.Warnings {
		fmt.Fprintf(&text, "%s %s\n", style.paint("Warning:", color.FgHiYellow), reportText(warning))
	}
	userCounts, assetCounts := report.Totals.UserActions, report.Totals.AssetResults
	fmt.Fprintf(&text, "%s %d total; %d create, %d update, %d move department, %d unchanged\n", style.paint("Users:", color.Bold), report.Totals.Users,
		userCounts[planner.UserCreate], userCounts[planner.UserUpdate], userCounts[planner.UserMoveDepartment], userCounts[planner.UserNoop])
	fmt.Fprintf(&text, "%s %d total; %d change, %d unchanged, %d skipped\n", style.paint("Assets:", color.Bold), report.Totals.Assets,
		assetCounts[planner.AssetChange], assetCounts[planner.AssetUnchanged], assetCounts[planner.AssetSkipped])
	if report.Apply != nil {
		failures := fmt.Sprintf("%d failures", len(report.Apply.Failures))
		if len(report.Apply.Failures) > 0 {
			failures = style.paint(failures, color.FgHiRed)
		}
		fmt.Fprintf(&text, "%s %d users, %d assets; %s\n", style.paint("Completed:", color.Bold), report.Apply.UsersApplied, report.Apply.AssetsApplied, failures)
	}
	users := make(map[string]app.UserResult)
	assets := make(map[string]app.AssetResult)
	if report.Apply != nil {
		for _, user := range report.Apply.Users {
			users[user.Email] = user
		}
		for _, asset := range report.Apply.Assets {
			assets[asset.SerialNumber] = asset
		}
	}
	for _, user := range plan.Users {
		action := string(user.Action)
		switch user.Action {
		case planner.UserCreate, planner.UserUpdate:
		case planner.UserNoop:
			action = "unchanged"
		case planner.UserMoveDepartment:
			action = "move department (absent from source)"
		}
		fmt.Fprintf(&text, "\n%s: %s\n", style.paint("User "+reportText(user.Email), color.Bold), style.paint(action, userColour(user.Action)))
		if user.Action == planner.UserNoop {
			continue
		}
		op := users[user.Email]
		operation := "patch"
		if user.Action == planner.UserCreate {
			operation = "create"
		}
		writeOperation(&text, style, operation, operationResult(report.Apply != nil, &op.OperationResult))
		writeUserFields(&text, user)
	}
	for _, asset := range plan.Assets {
		assetColour := color.FgHiYellow
		if asset.Result != planner.AssetChange {
			assetColour = color.Faint
		}
		fmt.Fprintf(&text, "\n%s: %s\n", style.paint("Asset "+reportText(asset.SerialNumber)+" ("+reportText(asset.Source)+")", color.Bold), style.paint(string(asset.Result), assetColour))
		if asset.SkipReason != "" {
			fmt.Fprintf(&text, "  Reason: %s\n", reportText(asset.SkipReason))
		}
		if asset.Note != "" {
			fmt.Fprintf(&text, "  Note: %s\n", reportText(asset.Note))
		}
		op := assets[asset.SerialNumber]
		if !asset.Patch.Empty() {
			writeOperation(&text, style, "patch", operationResult(report.Apply != nil, op.Patch))
			if asset.Patch.Name != nil {
				writeField(&text, "name", asset.CurrentName, *asset.Patch.Name)
			}
			if asset.Patch.ManagedBy != nil {
				writeField(&text, "managed_by", asset.CurrentManagedBy, *asset.Patch.ManagedBy)
			}
			if asset.Patch.StatusID != nil {
				writeField(&text, "status", asset.CurrentStatus, asset.DesiredStatus)
			}
		}
		if asset.Checkin {
			writeOperation(&text, style, "checkin", operationResult(report.Apply != nil, op.Checkin))
			writeField(&text, "assignment", asset.CurrentAssignment, "")
		}
		if asset.CheckoutUser != "" {
			writeOperation(&text, style, "checkout", operationResult(report.Apply != nil, op.Checkout))
			before := asset.CurrentAssignment
			if asset.Checkin {
				before = ""
			}
			writeField(&text, "assignment", before, asset.CheckoutUser)
		}
	}
	_, err := io.WriteString(writer, text.String())
	return err
}

func operationResult(apply bool, result *app.OperationResult) app.OperationResult {
	if !apply {
		return app.OperationResult{Status: "planned"}
	}
	if result == nil || result.Status == "" {
		return app.OperationResult{Status: "not_attempted"}
	}
	return *result
}

func writeOperation(text *strings.Builder, style textStyle, name string, result app.OperationResult) {
	status := strings.ReplaceAll(result.Status, "_", " ")
	if result.Error != "" {
		status += " (" + reportText(result.Error) + ")"
	}
	fmt.Fprintf(text, "  %s: %s\n", name, style.paint(status, operationColour(result.Status)))
}

func operationColour(status string) color.Attribute {
	switch status {
	case "applied":
		return color.FgHiGreen
	case "failed":
		return color.FgHiRed
	case "blocked", "not_attempted":
		return color.FgHiYellow
	}
	return color.Reset
}

func userColour(action planner.UserAction) color.Attribute {
	switch action {
	case planner.UserCreate:
		return color.FgHiGreen
	case planner.UserUpdate, planner.UserMoveDepartment:
		return color.FgHiYellow
	case planner.UserNoop:
		return color.Faint
	}
	return color.Reset
}

func writeUserFields(text *strings.Builder, user planner.UserPlan) {
	current := domain.TargetUser{}
	if user.Current != nil {
		current = *user.Current
	}
	for _, field := range []struct {
		name, before string
		after        *string
	}{
		{"given_name", current.GivenName, user.Patch.GivenName},
		{"surname", current.Surname, user.Patch.Surname},
		{"username", current.Username, user.Patch.Username},
		{"email", current.Email, user.Patch.Email},
		{"start_date", current.StartDate, user.Patch.StartDate},
	} {
		if field.after != nil {
			if user.Action == planner.UserCreate {
				fmt.Fprintf(text, "    %s: %s\n", field.name, strconv.Quote(*field.after))
			} else {
				writeField(text, field.name, field.before, *field.after)
			}
		}
	}
	for _, field := range []struct {
		name   string
		before int64
		after  *int64
	}{
		{"department_id", current.DepartmentID, user.Patch.DepartmentID},
		{"location_id", current.LocationID, user.Patch.LocationID},
	} {
		if field.after != nil {
			if user.Action == planner.UserCreate {
				fmt.Fprintf(text, "    %s: %d\n", field.name, *field.after)
			} else {
				fmt.Fprintf(text, "    %s: %d -> %d\n", field.name, field.before, *field.after)
			}
		}
	}
}

func writeField(text *strings.Builder, name, before, after string) {
	fmt.Fprintf(text, "    %s: %s -> %s\n", name, strconv.Quote(before), strconv.Quote(after))
}

// reportText escapes the characters in one value that could reshape a report
// or control a terminal. Printable text, including non-ASCII letters, is unchanged.
func reportText(text string) string {
	var result strings.Builder
	for _, r := range text {
		if strconv.IsPrint(r) {
			result.WriteRune(r)
		} else {
			quoted := strconv.QuoteRune(r)
			result.WriteString(quoted[1 : len(quoted)-1])
		}
	}
	return result.String()
}
