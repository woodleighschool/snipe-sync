package expression_test

import (
	"testing"

	"github.com/woodleighschool/snipe-sync/internal/domain"
	"github.com/woodleighschool/snipe-sync/internal/expression"
)

func TestCompilerChecksAndEvaluatesTypedPolicy(t *testing.T) {
	compiler, err := expression.NewAssetCompiler()
	if err != nil {
		t.Fatal(err)
	}
	program, err := compiler.CompileCondition(`device.source == "mdm" && asset.status == "Ready"`)
	if err != nil {
		t.Fatal(err)
	}
	matched, err := program.Eval(expression.Input{
		Device: &domain.Device{Source: "mdm"},
		Asset:  &domain.Asset{Status: "Ready"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expression did not match typed input")
	}
}

func TestCompilerRejectsNonBooleanPolicy(t *testing.T) {
	compiler, err := expression.NewUserCompiler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.CompileCondition(`user.user_principal_name`); err == nil {
		t.Fatal("CompileCondition accepted a string expression")
	}
}

func TestCompilersRejectVariablesOutsideTheirPolicyScope(t *testing.T) {
	t.Parallel()
	userCompiler, err := expression.NewUserCompiler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := userCompiler.CompileCondition(`device.name != ""`); err == nil {
		t.Fatal("user compiler accepted device variable")
	}
	assetCompiler, err := expression.NewAssetCompiler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := assetCompiler.CompileCondition(`user.present`); err == nil {
		t.Fatal("asset compiler accepted user variable")
	}
}
