package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/woodleighschool/snipe-sync/internal/app"
	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func TestWriteJSONPlanProducesOneCompleteObject(t *testing.T) {
	plan := outputFixture()
	var output bytes.Buffer
	if err := writePlan(&output, "json", plan); err != nil {
		t.Fatal(err)
	}
	var decoded planner.Plan
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Users) != 1 || len(decoded.Assets) != 2 {
		t.Errorf("decoded plan = %#v", decoded)
	}
}

func TestWriteRunResultSummarizesRoutineDevicesWithoutListingThem(t *testing.T) {
	var output bytes.Buffer
	result := app.Result{Plan: outputFixture(), Apply: &app.ApplyResult{AssetsApplied: 1}}
	if err := writeRunResult(&output, result); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, unwanted := range []string{"SERIAL-2", "missing in Snipe"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("run output contains routine detail %q:\n%s", unwanted, text)
		}
	}
	for _, want := range []string{
		"SERIAL-1",
		"Device summary: 2 total, 1 change, 0 unchanged, 1 skipped",
		"Applied users: 0; assets: 1; errors: 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("run output missing %q:\n%s", want, text)
		}
	}
}

func TestWriteHumanPlanUsesComparisonColumnsAndSummaries(t *testing.T) {
	var output bytes.Buffer
	if err := writePlan(&output, "human", outputFixture()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Users: 1 create, 0 update, 0 disable, 0 unchanged",
		"SOURCE", "SERIAL", "ASSIGNMENT", "MANAGED BY", "RESULT",
		"OLD → NEW", "old@example.invalid → new@example.invalid", "name, managed by, reassign",
		"Skip: missing in Snipe",
		"Device summary: 2 total, 1 change, 0 unchanged, 1 skipped",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("human plan missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "\t") {
		t.Errorf("human plan contains tabs: %q", text)
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
		},
	}
}
