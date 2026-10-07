package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func TestWriteJSONPlanProducesOneCompleteObject(t *testing.T) {
	plan := outputFixture()
	for _, all := range []bool{false, true} {
		var output bytes.Buffer
		if err := writeReport(&output, true, all, app.Result{Plan: &plan}, nil); err != nil {
			t.Fatal(err)
		}
		var decoded reconciliationReport
		if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Plan.Users) != 1 || len(decoded.Plan.Assets) != 4 {
			t.Errorf("all %t: decoded plan = %#v", all, decoded)
		}
	}
}

func TestWriteHumanPlanShowsChangesAndSkippedAssets(t *testing.T) {
	var output bytes.Buffer
	plan := outputFixture()
	if err := writeReport(&output, false, false, app.Result{Plan: &plan}, nil); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Users: 1 total; 1 create, 0 update, 0 move department, 0 unchanged",
		"User new@example.invalid: create", "Asset SERIAL-1 (primary): change",
		`name: "OLD" → "NEW"`, `assignment: "old@example.invalid" → ""`, "patch: planned", "checkin: planned", "checkout: planned",
		"Reason: missing in Snipe",
		"SERIAL-4", "Note: primary user unresolved; checkout preserved",
		"Assets: 4 total; 1 change, 2 unchanged, 1 skipped",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("human plan missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "SERIAL-3") {
		t.Errorf("human plan contains unchanged asset:\n%s", text)
	}
	if strings.Contains(text, "\t") {
		t.Errorf("human plan contains tabs: %q", text)
	}
}

func TestWriteHumanPlanAllIncludesUnchangedAssets(t *testing.T) {
	var output bytes.Buffer
	plan := outputFixture()
	if err := writeReport(&output, false, true, app.Result{Plan: &plan}, nil); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"SERIAL-3", "unchanged", "Assets: 4 total; 1 change, 2 unchanged, 1 skipped"} {
		if !strings.Contains(text, want) {
			t.Errorf("complete human plan missing %q:\n%s", want, text)
		}
	}
}

func outputFixture() planner.Plan {
	name := "NEW"
	managedBy := "Primary MDM"
	return planner.Plan{
		Users: []planner.UserPlan{{Email: "new@example.invalid", Action: planner.UserCreate}},
		Assets: []planner.AssetPlan{
			{
				Source: "primary", SerialNumber: "SERIAL-1", CurrentName: "OLD", DesiredName: "NEW",
				CurrentAssignment: "old@example.invalid", DesiredAssignment: "new@example.invalid",
				CurrentManagedBy: "Legacy", DesiredManagedBy: "Primary MDM",
				CurrentStatus: "Ready", DesiredStatus: "Ready",
				Patch: domain.AssetPatch{Name: &name, ManagedBy: &managedBy}, Checkin: true,
				CheckoutUser: "new@example.invalid", Result: planner.AssetChange,
			},
			{Source: "secondary", SerialNumber: "SERIAL-2", Result: planner.AssetSkipped, SkipReason: "missing in Snipe"},
			{Source: "primary", SerialNumber: "SERIAL-3", CurrentName: "CURRENT", DesiredName: "CURRENT", Result: planner.AssetUnchanged},
			{Source: "primary", SerialNumber: "SERIAL-4", CurrentName: "CURRENT", DesiredName: "CURRENT", Result: planner.AssetUnchanged, Note: "primary user unresolved; checkout preserved"},
		},
	}
}

func TestReportsUseIdenticalSelectionAndWholeRunTotals(t *testing.T) {
	plan := outputFixture()
	plan.Users = append(plan.Users, planner.UserPlan{Email: "unchanged@example.invalid", Action: planner.UserNoop})
	for _, all := range []bool{false, true} {
		var human, machine bytes.Buffer
		if err := writeReport(&human, false, all, app.Result{Plan: &plan}, nil); err != nil {
			t.Fatal(err)
		}
		if err := writeReport(&machine, true, all, app.Result{Plan: &plan}, nil); err != nil {
			t.Fatal(err)
		}
		var report reconciliationReport
		if err := json.Unmarshal(machine.Bytes(), &report); err != nil {
			t.Fatal(err)
		}
		if report.Totals.Users != 2 || report.Totals.Assets != 4 || report.Totals.UserActions[planner.UserNoop] != 1 || report.Totals.AssetResults[planner.AssetUnchanged] != 2 {
			t.Fatalf("filtered totals = %#v", report.Totals)
		}
		if strings.Contains(human.String(), "unchanged@example.invalid") != all || len(report.Plan.Users) != 2 {
			t.Fatalf("user selection differs: %s / %s", &human, &machine)
		}
		if strings.Contains(human.String(), "SERIAL-3") != all || len(report.Plan.Assets) != 4 {
			t.Fatalf("asset selection differs: %s / %s", &human, &machine)
		}
	}
	if len(plan.Users) != 2 || len(plan.Assets) != 4 {
		t.Fatal("rendering changed original plan")
	}
}

func TestReportShowsPartialWritesAndSafeUserDiffs(t *testing.T) {
	plan := outputFixture()
	plan.Warnings = []string{"Enrichment unavailable; location preserved"}
	plan.Users = []planner.UserPlan{{
		Email: "person@example.invalid", Action: planner.UserMoveDepartment,
		Current: &domain.TargetUser{GivenName: "Old", DepartmentID: 5},
		Patch:   domain.UserPatch{GivenName: new("New\n\x1b[31m\u00e9"), DepartmentID: new(int64(17))},
	}}
	applied := &app.ApplyResult{
		Users:  []app.UserResult{{Email: "person@example.invalid", Operation: "patch", Status: "not_attempted"}},
		Assets: []app.AssetResult{{SerialNumber: "SERIAL-1", Patch: &app.OperationResult{Status: "applied"}, Checkin: &app.OperationResult{Status: "applied"}, Checkout: &app.OperationResult{Status: "failed", Error: "request rejected"}}},
	}
	for _, jsonOutput := range []bool{false, true} {
		var output bytes.Buffer
		if err := writeReport(&output, jsonOutput, false, app.Result{Plan: &plan, Apply: applied}, errors.New("assignment failed")); err != nil {
			t.Fatal(err)
		}
		if jsonOutput {
			var report reconciliationReport
			if err := json.Unmarshal(output.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Error != "assignment failed" || report.Apply.Assets[0].Patch.Status != "applied" || report.Apply.Assets[0].Checkout.Status != "failed" {
				t.Fatalf("partial report: %s", &output)
			}
			continue
		}
		for _, want := range []string{"move department (absent from source)", `department_id: 5 → 17`, `given_name: "Old" → "New\n\x1b[31mé"`, "patch: applied", "checkin: applied", "checkout: failed (request rejected)", "patch: not attempted", "! Enrichment unavailable"} {
			if !strings.Contains(output.String(), want) {
				t.Errorf("report missing %q: %s", want, &output)
			}
		}
		for _, r := range output.String() {
			if r != '\n' && !strconv.IsPrint(r) {
				t.Fatalf("control rune %U in human report", r)
			}
		}
	}
}

func TestCreatedUsersShowInitialValues(t *testing.T) {
	plan := planner.Plan{Users: []planner.UserPlan{{
		Email: "new@example.invalid", Action: planner.UserCreate,
		Patch: domain.UserPatch{GivenName: new("New"), Email: new("new@example.invalid"), DepartmentID: new(int64(17))},
	}}}
	var output bytes.Buffer
	if err := writeReport(&output, false, false, app.Result{Plan: &plan}, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`given_name: "New"`, `email: "new@example.invalid"`, "department_id: 17"} {
		if !strings.Contains(output.String(), "\n    "+want+"\n") {
			t.Errorf("missing initial value %q: %s", want, &output)
		}
	}
}

func TestReportedFailurePreservesCauseWithConciseSummary(t *testing.T) {
	failure := errors.New("checkout rejected for SERIAL-1")
	plan := outputFixture()
	result := app.Result{Plan: &plan, Apply: &app.ApplyResult{Assets: []app.AssetResult{{SerialNumber: "SERIAL-1", Checkout: &app.OperationResult{Status: "failed", Error: failure.Error()}}}}}
	for _, cause := range []error{failure, errors.Join(failure, context.Canceled)} {
		var output bytes.Buffer
		err := reportResult(&output, false, false, result, cause)
		if !errors.Is(err, cause) || err.Error() != failure.Error() {
			t.Fatalf("reported failure = %v", err)
		}
		if !strings.Contains(output.String(), failure.Error()) {
			t.Fatalf("report lost failure: %s", &output)
		}
		if errors.Is(cause, context.Canceled) && (!errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "interrupted")) {
			t.Fatalf("lost cancellation: %v", err)
		}
	}
}

func TestReportWriteFailureRetainsUnreportedErrors(t *testing.T) {
	failure := errors.New("checkout rejected")
	writeErr := errors.New("stdout closed")
	plan := outputFixture()
	err := reportResult(errorWriter{err: writeErr}, false, false, app.Result{Plan: &plan, Apply: &app.ApplyResult{}}, failure)
	if !errors.Is(err, writeErr) || !errors.Is(err, failure) || !strings.Contains(err.Error(), "checkout rejected") || !strings.Contains(err.Error(), "write report: stdout closed") {
		t.Fatalf("write failure = %v", err)
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
