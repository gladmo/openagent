package yaml_test

// yamlv3_test.go pins the yaml.v3 semantics openagent depends on: all
// frontmatter in this repo is unmarshalled from user-authored YAML into
// either map[string]any or flat string/bool structs. The vendored upstream
// suite is not copied (it requires gopkg.in/check.v1); these cases keep the
// consumed subset observable here.

import (
	"reflect"
	"testing"

	yamlv3 "github.com/gladmo/openagent/yamlv3"
)

type skillFrontmatterShape struct {
	Name                   string `yaml:"name"`
	Description            string `yaml:"description"`
	DisableModelInvocation bool   `yaml:"disable-model-invocation"`
}

type templateFrontmatterShape struct {
	Description  string `yaml:"description"`
	ArgumentHint string `yaml:"argument-hint"`
}

func TestUnmarshalMapScalars(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want map[string]any
	}{
		{"plain strings", "name: test\ndescription: Review code\n", map[string]any{"name": "test", "description": "Review code"}},
		{"double-quoted string", "description: \"Review code\"", map[string]any{"description": "Review code"}},
		{"single-quoted string", "description: 'Review code'", map[string]any{"description": "Review code"}},
		{"int", "count: 3", map[string]any{"count": 3}},
		{"bool", "allowed: true", map[string]any{"allowed": true}},
		{"trailing comment", "# lead\ndescription: d # trail", map[string]any{"description": "d"}},
		{
			"nested mapping stays map[string]any",
			"metadata:\n  license: MIT\n",
			map[string]any{"metadata": map[string]any{"license": "MIT"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got map[string]any
			if err := yamlv3.Unmarshal([]byte(tc.in), &got); err != nil {
				t.Fatalf("Unmarshal(%q) error: %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Unmarshal(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// TestFlowSequenceIsNotAString pins the PORTING.md "YAML NOTE": a bare
// argument-hint like [file] resolves to a sequence, so a string assertion
// fails and the hint is dropped — hints must be quoted in fixtures.
func TestFlowSequenceIsNotAString(t *testing.T) {
	var got map[string]any
	if err := yamlv3.Unmarshal([]byte("argument-hint: [file]"), &got); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if _, ok := got["argument-hint"].(string); ok {
		t.Fatalf("argument-hint [file] decoded as string: %#v", got["argument-hint"])
	}
	if !reflect.DeepEqual(got["argument-hint"], []any{"file"}) {
		t.Fatalf("argument-hint = %#v, want []any{\"file\"}", got["argument-hint"])
	}
}

func TestUnmarshalFrontmatterStructs(t *testing.T) {
	var skill skillFrontmatterShape
	skillYaml := "name: code-review\ndescription: Review a PR\ndisable-model-invocation: true\n"
	if err := yamlv3.Unmarshal([]byte(skillYaml), &skill); err != nil {
		t.Fatalf("skill Unmarshal error: %v", err)
	}
	wantSkill := skillFrontmatterShape{Name: "code-review", Description: "Review a PR", DisableModelInvocation: true}
	if skill != wantSkill {
		t.Fatalf("skill = %+v, want %+v", skill, wantSkill)
	}

	var tmpl templateFrontmatterShape
	if err := yamlv3.Unmarshal([]byte("description: d\nargument-hint: \"[file]\"\n"), &tmpl); err != nil {
		t.Fatalf("template Unmarshal error: %v", err)
	}
	if tmpl.Description != "d" || tmpl.ArgumentHint != "[file]" {
		t.Fatalf("template = %+v, want {d [file]}", tmpl)
	}
}

func TestUnmarshalEmptyAndError(t *testing.T) {
	var empty map[string]any
	if err := yamlv3.Unmarshal([]byte(""), &empty); err != nil {
		t.Fatalf("empty document error: %v", err)
	}
	if empty != nil {
		t.Fatalf("empty document = %#v, want nil", empty)
	}
	var blank map[string]any
	if err := yamlv3.Unmarshal([]byte("\n  \n"), &blank); err != nil {
		t.Fatalf("blank document error: %v", err)
	}
	if blank != nil {
		t.Fatalf("blank document = %#v, want nil", blank)
	}

	var m map[string]any
	if err := yamlv3.Unmarshal([]byte("key: [unclosed"), &m); err == nil {
		t.Fatalf("unterminated flow sequence = %#v, want error", m)
	}

	var s skillFrontmatterShape
	if err := yamlv3.Unmarshal([]byte("description: [a, b]"), &s); err == nil {
		t.Fatalf("sequence into string field = %+v, want error", s)
	}
}
