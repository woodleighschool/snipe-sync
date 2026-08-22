package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v4"
)

var environmentPlaceholder = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// Load merges, substitutes, validates, and compiles configuration files in order.
func Load(paths ...string) (*Config, error) {
	return load(paths, os.LookupEnv)
}

func load(paths []string, lookup func(string) (string, bool)) (*Config, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("at least one config file is required")
	}

	var document yaml.Node
	for index, path := range paths {
		fragment, err := readDocument(path)
		if err != nil {
			return nil, err
		}
		if len(fragment.Content) != 1 || fragment.Content[0].Kind != yaml.MappingNode {
			return nil, fmt.Errorf("config %s must contain a YAML mapping", path)
		}
		if index == 0 {
			document = fragment
			continue
		}
		mergeNode(document.Content[0], fragment.Content[0])
	}

	if err := substituteEnvironment(&document, lookup); err != nil {
		return nil, err
	}

	var substituted bytes.Buffer
	encoder := yaml.NewEncoder(&substituted)
	if err := encoder.Encode(&document); err != nil {
		return nil, fmt.Errorf("prepare config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("prepare config: %w", err)
	}

	decoder := yaml.NewDecoder(&substituted)
	decoder.KnownFields(true)
	var config Config
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("decode config: %w", err)
		}
		return nil, fmt.Errorf("decode config: multiple YAML documents are not supported")
	}

	config.applyDefaults()
	if err := config.validateAndCompile(); err != nil {
		return nil, err
	}
	return &config, nil
}

func readDocument(path string) (yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return yaml.Node{}, fmt.Errorf("read config %s: %w", path, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return yaml.Node{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return yaml.Node{}, fmt.Errorf("parse config %s: %w", path, err)
		}
		return yaml.Node{}, fmt.Errorf("parse config %s: multiple YAML documents are not supported", path)
	}
	if err := rejectDuplicateKeys(&document); err != nil {
		return yaml.Node{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	return document, nil
}

func rejectDuplicateKeys(node *yaml.Node) error {
	if node.Kind == yaml.MappingNode {
		seen := make(map[string]struct{}, len(node.Content)/2)
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			identity := key.Tag + "\x00" + key.Value
			if _, exists := seen[identity]; exists {
				return fmt.Errorf("mapping key %q is duplicated", key.Value)
			}
			seen[identity] = struct{}{}
		}
	}
	for _, child := range node.Content {
		if err := rejectDuplicateKeys(child); err != nil {
			return err
		}
	}
	return nil
}

func mergeNode(base, overlay *yaml.Node) {
	if base.Kind != yaml.MappingNode || overlay.Kind != yaml.MappingNode {
		*base = *overlay
		return
	}

	for index := 0; index < len(overlay.Content); index += 2 {
		key := overlay.Content[index]
		value := overlay.Content[index+1]
		baseValue := mappingValue(base, key)
		if baseValue == nil {
			base.Content = append(base.Content, key, value)
			continue
		}
		mergeNode(baseValue, value)
	}
}

func mappingValue(mapping, key *yaml.Node) *yaml.Node {
	for index := 0; index < len(mapping.Content); index += 2 {
		candidate := mapping.Content[index]
		if candidate.Tag == key.Tag && candidate.Value == key.Value {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func substituteEnvironment(node *yaml.Node, lookup func(string) (string, bool)) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		match := environmentPlaceholder.FindStringSubmatch(node.Value)
		if len(match) == 2 {
			value, ok := lookup(match[1])
			if !ok {
				return fmt.Errorf("environment variable %s is not set", match[1])
			}
			node.Value = value
			return nil
		}
		if strings.Contains(node.Value, "${") {
			return fmt.Errorf("environment placeholders must occupy an entire YAML scalar")
		}
	}
	for _, child := range node.Content {
		if err := substituteEnvironment(child, lookup); err != nil {
			return err
		}
	}
	return nil
}
