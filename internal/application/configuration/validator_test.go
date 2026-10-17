package configuration

import (
	"bytes"
	"encoding/json"

	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	app "haeryong.local/haeryong/internal/application/storage"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

var _ = It("TestSchemaClosureMatchesSelectedMachineAuthority", Label("unit"), func() {

	b, e := os.ReadFile(filepath.Join("..", "..", "..", "tests", "contracts", "schema.json"))
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	var schema struct {
		Defs map[string]any `json:"$defs"`
	}
	{
		e = json.Unmarshal(b, &schema)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	for name, v := range definitions {
		Expect(!reflect.DeepEqual(v, schema.Defs[name])).To(BeFalse(), func() string { return fmt.Sprintf("embedded selected definition changed: %s", name) })

	}
})
var _ = It("TestJSONYAMLShareStrictEffectiveValidator", Label("unit"), func() {

	original, e := Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e = Validate(original)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

	yamlBytes, e := EncodeYAML(original)
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

	fromYAML, e := Decode(bytes.NewReader(yamlBytes), "yaml")
	Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	{

		e = Validate(fromYAML)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}
	Expect(!reflect.DeepEqual(original, fromYAML)).To(BeFalse(), func() string { return fmt.Sprint("JSON/YAML semantic values differ") })

	for _, bad := range []string{"a: 1\na: 2\n", "a: &v hi\nb: *v\n", "a: !!str hi\n", "a: !custom hi\n", "a: 1\n---\na: 2\n", "a: {<<: {b: 1}}\n"} {
		{
			_, e = Decode(strings.NewReader(bad), "yaml")
			Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprintf("unsafe YAML accepted: %q", bad) })
		}

	}
	{
		_, e = Decode(strings.NewReader(`{"network":{},"network":{}}`), "json")
		Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprint("duplicate JSON accepted") })
	}

	for _, change := range []map[string]any{{"size_limit": json.Number("1")}, {"security": map[string]any{"session_idle_seconds": json.Number("0")}}, {"network": map[string]any{"listener": map[string]any{"port": json.Number("1023")}}}, {"maintenance": map[string]any{"schedule": map[string]any{"mode": "calendar", "cron": "99 * * * *"}}}, {"retention": map[string]any{"trash": "0d"}}} {
		{
			e = Validate(Merge(original, change))
			Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprintf("invalid change accepted: %v", change) })
		}

	}
	valid := Merge(original, map[string]any{"security": map[string]any{"remember_device_default": false}, "maintenance": map[string]any{"schedule": map[string]any{"mode": "calendar", "cron": "*/5 0-23 * * 0,6"}}})
	{
		e = Validate(valid)
		Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
	}

})
var _ = It("TestIntegerLexemesAndExplicitIANATimezones", Label("unit"), func() {

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
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			{

				e = Validate(Merge(base, change))
				Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprintf("non-integer %s lexeme accepted: %s", format, lexeme) })
			}

		}
		for _, zone := range []string{"Local", "UTC", "Asia/Seoul"} {
			raw := `{"maintenance":{"schedule":{"timezone":"` + zone + `"}}}`
			if format == "yaml" {
				raw = "maintenance:\n  schedule:\n    timezone: " + zone + "\n"
			}
			change, e := Decode(strings.NewReader(raw), format)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			base, e := Decode(bytes.NewReader(app.DefaultConfiguration()), "json")
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			value := Merge(base, change)
			e = Validate(value)
			if zone == "Local" {
				Expect(e == nil).To(BeFalse(), func() string { return fmt.Sprint("host-local timezone accepted as explicit IANA zone", format) })

				continue
			}
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(zone, e) })

			exported, e := EncodeYAML(value)
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })

			roundtrip, e := Decode(bytes.NewReader(exported), "yaml")
			Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			{

				e = Validate(roundtrip)
				Expect(e != nil).To(BeFalse(), func() string { return fmt.Sprint(e) })
			}

		}
	}
})
