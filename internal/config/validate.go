package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/woodleighschool/snipe-sync/internal/expression"
)

const supportedVersion = 1

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

func (c *Config) applyDefaults() {
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if !c.Reconcile.PollInterval.set {
		c.Reconcile.PollInterval.Duration = time.Minute
	}
	if strings.TrimSpace(c.Users.IncludeWhen) == "" {
		c.Users.IncludeWhen = "true"
	}
	if strings.TrimSpace(c.Assets.Assignment.SharedWhen) == "" {
		c.Assets.Assignment.SharedWhen = "false"
	}
	for index := range c.Devices {
		if strings.TrimSpace(c.Devices[index].ManagedBy) == "" {
			c.Devices[index].ManagedBy = c.Devices[index].Name
		}
	}
}

func (c *Config) normalize() {
	c.LogLevel = strings.ToLower(strings.TrimSpace(c.LogLevel))
}

func (c *Config) validateAndCompile() error {
	if c.Version != supportedVersion {
		return fmt.Errorf("config version must be %d, found %d", supportedVersion, c.Version)
	}
	if err := c.validateLogLevel(); err != nil {
		return err
	}
	if c.Reconcile.PollInterval.Duration <= 0 {
		return fmt.Errorf("reconcile.poll_interval must be greater than zero")
	}
	if err := c.validateConnections(); err != nil {
		return err
	}
	if err := c.validateIdentity(); err != nil {
		return err
	}
	if err := c.validateDevices(); err != nil {
		return err
	}
	if err := c.validateTarget(); err != nil {
		return err
	}
	return c.validatePolicy()
}

func (c *Config) validateLogLevel() error {
	switch c.LogLevel {
	case "debug":
		c.ParsedLevel = slog.LevelDebug
	case "info":
		c.ParsedLevel = slog.LevelInfo
	case "warn":
		c.ParsedLevel = slog.LevelWarn
	case "error":
		c.ParsedLevel = slog.LevelError
	default:
		return fmt.Errorf("log_level must be debug, info, warn, or error")
	}
	return nil
}

func (c *Config) validateConnections() error {
	if len(c.Connections) == 0 {
		return fmt.Errorf("connections must define at least one connection")
	}
	for _, name := range sortedKeys(c.Connections) {
		connection := c.Connections[name]
		if !identifierPattern.MatchString(name) {
			return fmt.Errorf("connections.%s: name must match %s", name, identifierPattern)
		}
		switch connection.Type {
		case "microsoft_graph":
			if strings.TrimSpace(connection.TenantID) == "" || strings.TrimSpace(connection.ClientID) == "" || strings.TrimSpace(connection.ClientSecret) == "" {
				return fmt.Errorf("connections.%s: tenant_id, client_id, and client_secret are required", name)
			}
			if connection.URL != "" || connection.APIKey != "" {
				return fmt.Errorf("connections.%s: url and api_key are not valid for microsoft_graph", name)
			}
			if connection.BaseURL != "" {
				if err := validateHTTPURL(connection.BaseURL); err != nil {
					return fmt.Errorf("connections.%s.base_url: %w", name, err)
				}
			}
		case "jamf":
			if err := validateHTTPURL(connection.URL); err != nil {
				return fmt.Errorf("connections.%s.url: %w", name, err)
			}
			if strings.TrimSpace(connection.ClientID) == "" || strings.TrimSpace(connection.ClientSecret) == "" {
				return fmt.Errorf("connections.%s: client_id and client_secret are required", name)
			}
			if connection.TenantID != "" || connection.APIKey != "" || connection.BaseURL != "" {
				return fmt.Errorf("connections.%s: tenant_id, api_key, and base_url are not valid for jamf", name)
			}
		case "snipeit":
			if err := validateHTTPURL(connection.URL); err != nil {
				return fmt.Errorf("connections.%s.url: %w", name, err)
			}
			if strings.TrimSpace(connection.APIKey) == "" {
				return fmt.Errorf("connections.%s.api_key is required", name)
			}
			if connection.TenantID != "" || connection.ClientID != "" || connection.ClientSecret != "" || connection.BaseURL != "" {
				return fmt.Errorf("connections.%s: tenant_id, client_id, client_secret, and base_url are not valid for snipeit", name)
			}
		default:
			return fmt.Errorf("connections.%s.type %q is not supported", name, connection.Type)
		}
	}
	return nil
}

func (c *Config) validateIdentity() error {
	if !identifierPattern.MatchString(c.Identity.Name) {
		return fmt.Errorf("identity.name must match %s", identifierPattern)
	}
	if c.Identity.Type != "entra" {
		return fmt.Errorf("identity.type %q is not supported", c.Identity.Type)
	}
	connection, ok := c.Connections[c.Identity.Connection]
	if !ok {
		return fmt.Errorf("identity.connection %q does not exist", c.Identity.Connection)
	}
	if connection.Type != "microsoft_graph" {
		return fmt.Errorf("identity requires a microsoft_graph connection")
	}
	if len(c.Identity.Domains) == 0 {
		return fmt.Errorf("identity.domains must not be empty")
	}
	seenDomains := make(map[string]struct{}, len(c.Identity.Domains))
	for index, domain := range c.Identity.Domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" || strings.ContainsAny(domain, "@/ ") {
			return fmt.Errorf("identity.domains[%d] must be a bare DNS domain", index)
		}
		if _, exists := seenDomains[domain]; exists {
			return fmt.Errorf("identity.domains contains duplicate %q", domain)
		}
		seenDomains[domain] = struct{}{}
		c.Identity.Domains[index] = domain
	}
	for _, alias := range sortedKeys(c.Identity.Groups) {
		if !identifierPattern.MatchString(alias) {
			return fmt.Errorf("identity.groups.%s: alias must match %s", alias, identifierPattern)
		}
		if err := validateNonEmptyUnique("identity.groups."+alias, c.Identity.Groups[alias], false); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) validateDevices() error {
	if len(c.Devices) == 0 {
		return fmt.Errorf("devices must define at least one source")
	}
	seenNames := make(map[string]struct{}, len(c.Devices))
	seenTypes := make(map[string]struct{}, len(c.Devices))
	seenPriorities := make(map[int]struct{}, len(c.Devices))
	for index, source := range c.Devices {
		path := fmt.Sprintf("devices[%d]", index)
		if !identifierPattern.MatchString(source.Name) {
			return fmt.Errorf("%s.name must match %s", path, identifierPattern)
		}
		if _, exists := seenNames[source.Name]; exists {
			return fmt.Errorf("%s.name %q is duplicated", path, source.Name)
		}
		seenNames[source.Name] = struct{}{}
		if strings.TrimSpace(source.ManagedBy) == "" {
			return fmt.Errorf("%s.managed_by must not be empty", path)
		}
		if _, exists := seenTypes[source.Type]; exists {
			return fmt.Errorf("%s.type %q is configured more than once", path, source.Type)
		}
		seenTypes[source.Type] = struct{}{}
		if _, exists := seenPriorities[source.Priority]; exists {
			return fmt.Errorf("%s.priority %d is duplicated", path, source.Priority)
		}
		seenPriorities[source.Priority] = struct{}{}
		connection, ok := c.Connections[source.Connection]
		if !ok {
			return fmt.Errorf("%s.connection %q does not exist", path, source.Connection)
		}
		switch source.Type {
		case "intune":
			if connection.Type != "microsoft_graph" {
				return fmt.Errorf("%s requires a microsoft_graph connection", path)
			}
		case "jamf":
			if connection.Type != "jamf" {
				return fmt.Errorf("%s requires a jamf connection", path)
			}
		default:
			return fmt.Errorf("%s.type %q is not supported", path, source.Type)
		}
	}
	return nil
}

func (c *Config) validateTarget() error {
	if c.Target.Type != "snipeit" {
		return fmt.Errorf("target.type %q is not supported", c.Target.Type)
	}
	connection, ok := c.Connections[c.Target.Connection]
	if !ok {
		return fmt.Errorf("target.connection %q does not exist", c.Target.Connection)
	}
	if connection.Type != "snipeit" {
		return fmt.Errorf("target requires a snipeit connection")
	}
	if _, err := time.LoadLocation(c.Target.Timezone); err != nil {
		return fmt.Errorf("target.timezone: %w", err)
	}
	return nil
}

func (c *Config) validatePolicy() error {
	if strings.TrimSpace(c.Users.Absent.Department) == "" {
		return fmt.Errorf("users.absent.department is required")
	}
	if err := validateNonEmptyUnique("assets.manufacturers", c.Assets.Manufacturers, true); err != nil {
		return err
	}
	if err := validateNonEmptyUnique("assets.statuses.writable", c.Assets.Statuses.Writable, true); err != nil {
		return err
	}
	if len(c.Assets.Statuses.Promote.From) != 0 {
		if err := validateNonEmptyUnique("assets.statuses.promote.from", c.Assets.Statuses.Promote.From, true); err != nil {
			return err
		}
		if strings.TrimSpace(c.Assets.Statuses.Promote.To) == "" {
			return fmt.Errorf("assets.statuses.promote.to is required when from is configured")
		}
	} else if c.Assets.Statuses.Promote.To != "" {
		return fmt.Errorf("assets.statuses.promote.from is required when to is configured")
	}
	if strings.TrimSpace(c.Assets.ManagedByField) == "" {
		return fmt.Errorf("assets.managed_by_field is required")
	}
	userCompiler, err := expression.NewUserCompiler()
	if err != nil {
		return err
	}
	programs := Programs{}
	programs.UserInclude, err = userCompiler.CompileCondition(c.Users.IncludeWhen)
	if err != nil {
		return fmt.Errorf("users.include_when: %w", err)
	}
	programs.Locations = make([]expression.Program, 0, len(c.Users.Location.Cases))
	for index, locationCase := range c.Users.Location.Cases {
		path := fmt.Sprintf("users.location.cases[%d]", index)
		if strings.TrimSpace(locationCase.Value) == "" {
			return fmt.Errorf("%s.value is required", path)
		}
		program, compileErr := userCompiler.CompileCondition(locationCase.When)
		if compileErr != nil {
			return fmt.Errorf("%s.when: %w", path, compileErr)
		}
		programs.Locations = append(programs.Locations, program)
	}
	assetCompiler, err := expression.NewAssetCompiler()
	if err != nil {
		return err
	}
	programs.SharedAsset, err = assetCompiler.CompileCondition(c.Assets.Assignment.SharedWhen)
	if err != nil {
		return fmt.Errorf("assets.assignment.shared_when: %w", err)
	}
	c.Programs = programs
	return nil
}

func validateNonEmptyUnique(path string, values []string, foldCase bool) error {
	if len(values) == 0 {
		return fmt.Errorf("%s must not be empty", path)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			return fmt.Errorf("%s contains an empty value", path)
		}
		if foldCase {
			key = strings.ToLower(key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%s contains duplicate %q", path, value)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateHTTPURL(rawURL string) error {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return fmt.Errorf("must be an absolute HTTP URL")
	}
	return nil
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
