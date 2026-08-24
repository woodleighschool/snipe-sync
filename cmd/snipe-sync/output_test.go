package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/planner"
)

func TestWriteJSONPlanProducesOneCompleteObject(t *testing.T) {
	plan := outputFixture()
	for _, all := range []bool{false, true} {
		var output bytes.Buffer
		if err := writePlan(&output, "json", all, plan); err != nil {
			t.Fatal(err)
		}
		var decoded planner.Plan
		if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Users) != 1 || len(decoded.Assets) != 4 {
			t.Errorf("all %t: decoded plan = %#v", all, decoded)
		}
	}
}

func TestWriteHumanPlanShowsChangesAndSkippedAssets(t *testing.T) {
	var output bytes.Buffer
	if err := writePlan(&output, "human", false, outputFixture()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{
		"Users: 1 create, 0 update, 0 disable, 0 unchanged",
		"SOURCE", "SERIAL", "ASSIGNMENT", "MANAGED BY", "RESULT",
		"OLD → NEW", "old@example.invalid → new@example.invalid", "name, managed by, reassign",
		"Skip: missing in Snipe",
		"SERIAL-4", "No change; primary user unresolved; checkout preserved",
		"Device summary: 4 total, 1 change, 2 unchanged, 1 skipped",
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
	if err := writePlan(&output, "human", true, outputFixture()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"SERIAL-3", "No change", "Device summary: 4 total, 1 change, 2 unchanged, 1 skipped"} {
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
