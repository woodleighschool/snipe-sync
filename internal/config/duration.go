package config

import (
	"fmt"
	"strings"
	"time"

	"go.yaml.in/yaml/v4"
)

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a string")
	}
	return d.UnmarshalText([]byte(node.Value))
}

// UnmarshalText parses a Go duration from a configuration value.
func (d *Duration) UnmarshalText(text []byte) error {
	value := strings.TrimSpace(string(text))
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value, err)
	}
	d.Duration = parsed
	d.set = true
	return nil
}
