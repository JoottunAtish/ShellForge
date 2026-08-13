package content

import (
	"strings"
	"testing"
)

func TestUnmarshalDecodesYAML(t *testing.T) {
	var d struct {
		ID string `yaml:"id"`
	}
	if err := Unmarshal("nav-01.yaml", []byte("id: nav-01\n"), &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if d.ID != "nav-01" {
		t.Errorf("ID = %q, want %q", d.ID, "nav-01")
	}
}

func TestUnmarshalNamesTheFileOnError(t *testing.T) {
	var d struct {
		ID string `yaml:"id"`
	}
	err := Unmarshal("broken.yaml", []byte("id: [unterminated\n"), &d)
	if err == nil {
		t.Fatal("Unmarshal accepted malformed YAML")
	}
	if !strings.Contains(err.Error(), "broken.yaml") {
		t.Errorf("error does not name the file: %v", err)
	}
}

func TestUnmarshalRejectsUnknownFields(t *testing.T) {
	var d struct {
		ID string `yaml:"id"`
	}
	err := Unmarshal("nav-01.yaml", []byte("id: nav-01\nbogus: true\n"), &d)
	if err == nil {
		t.Fatal("Unmarshal silently accepted a field the target type does not declare")
	}
}
