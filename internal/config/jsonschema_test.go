package config_test

import (
	"bytes"
	"testing"

	"github.com/woodleighschool/snipe-sync/internal/config"
)

func TestJSONSchemaDocumentIsStable(t *testing.T) {
	first, err := config.JSONSchemaDocument()
	if err != nil {
		t.Fatal(err)
	}
	second, err := config.JSONSchemaDocument()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("JSON schema generation is not deterministic")
	}
	if !bytes.Contains(first, []byte("Configuration")) {
		t.Fatal("JSON schema is missing its title")
	}
}
