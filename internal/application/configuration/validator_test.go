package configuration

import (
	"bytes"
	"encoding/json"

	app "haeryong.local/haeryong/internal/application/storage"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSchemaClosureMatchesSelectedMachineAuthority(t *testing.T) {
	b, e := os.ReadFile(filepath.Join("..", "..", "..", "tests", "contracts", "schema.json"))
	if e != nil {
		t.Fatal(e)
	}
	var schema struct {
		Defs map[string]any `json:"$defs"`
	}
	if e = json.Unmarshal(b, &schema); e != nil {
		t.Fatal(e)
	}
	for name, v := range definitions {
		if !reflect.DeepEqual(v, schema.Defs[name]) {
			t.Errorf("embedded selected definition changed: %s", name)
		}
	}
}
func TestJSONYAMLShareStrictEffectiveValidator(t *testing.T) {
	original, e := Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
	if e != nil {
		t.Fatal(e)
	}
	if e = Validate(original); e != nil {
		t.Fatal(e)
	}
	yamlBytes, e := EncodeYAML(original)
	if e != nil {
		t.Fatal(e)
	}
	fromYAML, e := Decode(bytes.NewReader(yamlBytes), "yaml")
	if e != nil {
		t.Fatal(e)
	}
	if e = Validate(fromYAML); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(original, fromYAML) {
		t.Fatal("JSON/YAML semantic values differ")
	}
	for _, bad := range []string{"a: 1\na: 2\n", "a: &v hi\nb: *v\n", "a: !!str hi\n", "a: !custom hi\n", "a: 1\n---\na: 2\n", "a: {<<: {b: 1}}\n"} {
		if _, e = Decode(strings.NewReader(bad), "yaml"); e == nil {
			t.Errorf("unsafe YAML accepted: %q", bad)
		}
	}
	if _, e = Decode(strings.NewReader(`{"network":{},"network":{}}`), "json"); e == nil {
		t.Fatal("duplicate JSON accepted")
	}
	for _, change := range []map[string]any{{"size_limit": json.Number("1")}, {"security": map[string]any{"session_idle_seconds": json.Number("0")}}, {"network": map[string]any{"listener": map[string]any{"port": json.Number("1023")}}}, {"maintenance": map[string]any{"schedule": map[string]any{"mode": "calendar", "cron": "99 * * * *"}}}, {"retention": map[string]any{"trash": "0d"}}} {
		if e = Validate(Merge(original, change)); e == nil {
			t.Errorf("invalid change accepted: %v", change)
		}
	}
	valid := Merge(original, map[string]any{"security": map[string]any{"remember_device_default": false}, "maintenance": map[string]any{"schedule": map[string]any{"mode": "calendar", "cron": "*/5 0-23 * * 0,6"}}})
	if e = Validate(valid); e != nil {
		t.Fatal(e)
	}
}

func TestIntegerLexemesAndExplicitIANATimezones(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		for _, lexeme := range []string{"20.0", "2e1"} {
			raw := `{"maintenance":{"max_provider_attempts_per_run":` + lexeme + `}}`
			if format == "yaml" {
				raw = "maintenance:\n  max_provider_attempts_per_run: " + lexeme + "\n"
			}
			change, e := Decode(strings.NewReader(raw), format)
			if e != nil {
				continue
			}
			base, e := Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
			if e != nil {
				t.Fatal(e)
			}
			if e = Validate(Merge(base, change)); e == nil {
				t.Errorf("non-integer %s lexeme accepted: %s", format, lexeme)
			}
		}
		for _, zone := range []string{"Local", "UTC", "Asia/Seoul"} {
			raw := `{"maintenance":{"schedule":{"timezone":"` + zone + `"}}}`
			if format == "yaml" {
				raw = "maintenance:\n  schedule:\n    timezone: " + zone + "\n"
			}
			change, e := Decode(strings.NewReader(raw), format)
			if e != nil {
				t.Fatal(e)
			}
			base, e := Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
			if e != nil {
				t.Fatal(e)
			}
			value := Merge(base, change)
			e = Validate(value)
			if zone == "Local" {
				if e == nil {
					t.Error("host-local timezone accepted as explicit IANA zone", format)
				}
				continue
			}
			if e != nil {
				t.Fatal(zone, e)
			}
			exported, e := EncodeYAML(value)
			if e != nil {
				t.Fatal(e)
			}
			roundtrip, e := Decode(bytes.NewReader(exported), "yaml")
			if e != nil {
				t.Fatal(e)
			}
			if e = Validate(roundtrip); e != nil {
				t.Fatal(e)
			}
		}
	}
}
