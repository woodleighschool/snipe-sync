// Package expression compiles the typed CEL policy surface used by SnipeSync.
package expression

import (
	"fmt"
	"reflect"
	"strings"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/ext"

	"github.com/woodleighschool/snipe-sync/internal/domain"
)

const (
	userTypeName   = "domain.User"
	deviceTypeName = "domain.Device"
	assetTypeName  = "domain.Asset"
)

type scope uint8

const (
	userScope scope = iota
	assetScope
)

// Compiler validates policy expressions against SnipeSync's typed domain.
type Compiler struct {
	environment *cel.Env
	scope       scope
}

// Program is a compiled, thread-safe boolean CEL expression.
type Program struct {
	source  string
	program cel.Program
	scope   scope
}

// Input contains the values available to policy expressions.
type Input struct {
	User   *domain.User
	Device *domain.Device
	Asset  *domain.Asset
}

// NewUserCompiler creates the CEL environment for user selection and location policy.
func NewUserCompiler() (*Compiler, error) {
	return newCompiler(userScope)
}

// NewAssetCompiler creates the CEL environment for device and asset assignment policy.
func NewAssetCompiler() (*Compiler, error) {
	return newCompiler(assetScope)
}

func newCompiler(expressionScope scope) (*Compiler, error) {
	options := []cel.EnvOption{ext.Strings()}
	switch expressionScope {
	case userScope:
		options = append(options,
			ext.NativeTypes(reflect.TypeFor[domain.User](), ext.ParseStructTags(true)),
			cel.Variable("user", cel.ObjectType(userTypeName)),
		)
	case assetScope:
		options = append(options,
			ext.NativeTypes(
				reflect.TypeFor[domain.Device](),
				reflect.TypeFor[domain.Asset](),
				ext.ParseStructTags(true),
			),
			cel.Variable("device", cel.ObjectType(deviceTypeName)),
			cel.Variable("asset", cel.ObjectType(assetTypeName)),
		)
	default:
		return nil, fmt.Errorf("CEL scope %d is not supported", expressionScope)
	}
	environment, err := cel.NewEnv(options...)
	if err != nil {
		return nil, fmt.Errorf("create CEL environment: %w", err)
	}
	return &Compiler{environment: environment, scope: expressionScope}, nil
}

// CompileCondition compiles a boolean policy expression.
func (c *Compiler) CompileCondition(source string) (Program, error) {
	if strings.TrimSpace(source) == "" {
		return Program{}, fmt.Errorf("expression cannot be empty")
	}
	ast, issues := c.environment.Compile(source)
	if issues != nil && issues.Err() != nil {
		return Program{}, fmt.Errorf("compile CEL expression: %w", issues.Err())
	}
	if !ast.OutputType().IsExactType(cel.BoolType) {
		return Program{}, fmt.Errorf("CEL expression returns %s, want bool", ast.OutputType())
	}
	program, err := c.environment.Program(ast)
	if err != nil {
		return Program{}, fmt.Errorf("build CEL program: %w", err)
	}
	return Program{source: source, program: program, scope: c.scope}, nil
}

// Eval evaluates the expression with provider-neutral input.
func (p Program) Eval(input Input) (bool, error) {
	variables := make(map[string]any, 2)
	switch p.scope {
	case userScope:
		if input.User == nil {
			input.User = &domain.User{}
		}
		variables["user"] = input.User
	case assetScope:
		if input.Device == nil {
			input.Device = &domain.Device{}
		}
		if input.Asset == nil {
			input.Asset = &domain.Asset{}
		}
		variables["device"] = input.Device
		variables["asset"] = input.Asset
	default:
		return false, fmt.Errorf("evaluate CEL expression %q: invalid scope", p.source)
	}
	value, _, err := p.program.Eval(variables)
	if err != nil {
		return false, fmt.Errorf("evaluate CEL expression %q: %w", p.source, err)
	}
	result, ok := value.Value().(bool)
	if !ok {
		return false, fmt.Errorf("evaluate CEL expression %q: returned %T, want bool", p.source, value.Value())
	}
	return result, nil
}
