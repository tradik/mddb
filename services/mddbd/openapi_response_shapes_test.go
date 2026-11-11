package main

import (
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The spec described some answers as a bare `"200": OK`, so a generated
// client got an untyped blob and a reader had to guess the fields. This guard
// holds each listed path's 200 schema to the Go struct the handler answers
// with: every JSON field the server writes is a documented property.

type openAPISpec struct {
	Paths      map[string]map[string]yaml.Node `yaml:"paths"` // operations, and path-level parameters
	Components struct {
		Schemas map[string]openAPISchema `yaml:"schemas"`
	} `yaml:"components"`
}

type openAPIOperation struct {
	Responses map[string]struct {
		Content map[string]struct {
			Schema openAPISchema `yaml:"schema"`
		} `yaml:"content"`
	} `yaml:"responses"`
}

type openAPISchema struct {
	Ref        string                   `yaml:"$ref"`
	Properties map[string]openAPISchema `yaml:"properties"`
	Items      *openAPISchema           `yaml:"items"`
}

func loadOpenAPISpec(t *testing.T) openAPISpec {
	t.Helper()
	raw, err := os.ReadFile("../../docs/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec openAPISpec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// operation decodes one method of one path; the zero value when absent.
func (s openAPISpec) operation(t *testing.T, path, method string) openAPIOperation {
	t.Helper()
	var op openAPIOperation
	if node, ok := s.Paths[path][method]; ok {
		if err := node.Decode(&op); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return op
}

// resolve follows a local $ref to its component schema.
func (s openAPISpec) resolve(sc openAPISchema) openAPISchema {
	if name, found := strings.CutPrefix(sc.Ref, "#/components/schemas/"); found {
		return s.Components.Schemas[name]
	}
	return sc
}

// jsonFields lists the JSON names a struct marshals to, embedded structs
// included.
func jsonFields(t reflect.Type) []string {
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Anonymous && name == "" {
			out = append(out, jsonFields(f.Type)...)
			continue
		}
		if name == "-" || !f.IsExported() {
			continue
		}
		out = append(out, name)
	}
	return out
}

func missingProperties(sc openAPISchema, v any) []string {
	var missing []string
	for _, name := range jsonFields(reflect.TypeOf(v)) {
		if _, ok := sc.Properties[name]; !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func TestOpenAPIResponsesDescribeWhatTheHandlersWrite(t *testing.T) {
	spec := loadOpenAPISpec(t)
	for path, answer := range map[string]any{
		"/v1/geo-search":       GeoSearchResponse{},
		"/v1/geo-stats":        GeoStatsResponse{},
		"/v1/geo-within":       GeoWithinResponse{},
		"/v1/geo-reindex":      GeoReindexResponse{},
		"/v1/geo-encode":       GeoEncodeResponse{},
		"/v1/geo-decode":       GeoDecodeResponse{},
		"/v1/find-duplicates":  FindDuplicatesResponse{},
		"/v1/memory/sessions":  MemorySessionsListResponse{},
		"/v1/bulk-ingest-jobs": struct{ Jobs, Total int }{},
	} {
		op := spec.operation(t, path, "post")
		if op.Responses == nil {
			op = spec.operation(t, path, "get")
		}
		media, ok := op.Responses["200"].Content["application/json"]
		if !ok {
			t.Errorf("%s: the 200 answer has no application/json schema", path)
			continue
		}
		sc := spec.resolve(media.Schema)
		if path == "/v1/bulk-ingest-jobs" {
			for _, name := range []string{"jobs", "total"} {
				if _, ok := sc.Properties[name]; !ok {
					t.Errorf("%s: %s undocumented", path, name)
				}
			}
			continue
		}
		if missing := missingProperties(sc, answer); len(missing) > 0 {
			t.Errorf("%s: written but undocumented: %v", path, missing)
		}
	}

	// The documents of a duplicate group and of a geo result, one level down.
	if missing := missingProperties(spec.Components.Schemas["DuplicateDocInfo"], DuplicateDocInfo{}); len(missing) > 0 {
		t.Errorf("DuplicateDocInfo: written but undocumented: %v", missing)
	}
	within := spec.resolve(spec.operation(t, "/v1/geo-within", "post").Responses["200"].Content["application/json"].Schema)
	if items := within.Properties["results"].Items; items == nil {
		t.Error("/v1/geo-within: results has no item schema")
	} else if missing := missingProperties(spec.resolve(*items), GeoSearchResultItem{}); len(missing) > 0 {
		t.Errorf("/v1/geo-within results: written but undocumented: %v", missing)
	}
}
