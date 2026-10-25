package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/msvens/mfide/api"
	"github.com/msvens/mfide/internal/dao"
	"go.uber.org/zap"
)

// The OpenAPI document is hand-written; these tests keep it honest against the
// code: routes, response fields and their types, and parameter rules.

type spec = map[string]any

func loadSpec(t *testing.T) spec {
	t.Helper()
	var s spec
	if err := json.Unmarshal(api.OpenAPI, &s); err != nil {
		t.Fatalf("api/openapi.json is not valid JSON: %v", err)
	}
	return s
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func list(v any) []any         { l, _ := v.([]any); return l }

// at walks a path of keys, e.g. at(s, "components", "schemas", "Player").
func at(v any, keys ...string) map[string]any {
	m := obj(v)
	for _, k := range keys {
		m = obj(m[k])
	}
	return m
}

// deref resolves a local "$ref".
func deref(sp spec, m map[string]any) map[string]any {
	ref, ok := m["$ref"].(string)
	if !ok {
		return m
	}
	return at(sp, strings.Split(strings.TrimPrefix(ref, "#/"), "/")...)
}

func TestOpenAPIRoutesMatchServer(t *testing.T) {
	sp := loadSpec(t)
	srv := newServer("/api", nil, nil, nil, zap.NewNop().Sugar())
	var paths []string
	for p, ops := range at(sp, "paths") {
		paths = append(paths, p)
		for method := range obj(ops) {
			if method != "get" {
				t.Errorf("%s: only GET routes exist, spec has %s", p, method)
			}
		}
	}
	slices.Sort(paths)
	routes := slices.Sorted(slices.Values(srv.patterns))
	if !slices.Equal(paths, routes) {
		t.Errorf("spec paths %v\nserver routes %v", paths, routes)
	}
}

func TestOpenAPIIsServedAsIs(t *testing.T) {
	srv := newServer("/api", nil, nil, nil, zap.NewNop().Sugar())
	rec := httptest.NewRecorder()
	srv.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != string(api.OpenAPI) {
		t.Errorf("status %d; body equals the repo file: %v", rec.Code, rec.Body.String() == string(api.OpenAPI))
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type %q", ct)
	}
}

// schemaOf maps response structs to the schema documenting them.
var schemaOf = map[reflect.Type]string{
	reflect.TypeFor[ratingJSON](): "Rating",
	reflect.TypeFor[playerJSON](): "Player",
	reflect.TypeFor[monthJSON]():  "HistoryEntry",
	reflect.TypeFor[health]():     "Health",
	reflect.TypeFor[syncInfo]():   "SyncInfo",
	reflect.TypeFor[keyInfo]():    "KeyInfo",
}

func TestOpenAPISchemasMatchStructs(t *testing.T) {
	sp := loadSpec(t)
	for typ, name := range schemaOf {
		checkStruct(t, typ, name, at(sp, "components", "schemas", name))
	}
	// TopEntry is a Player plus rank, like topJSON embeds playerJSON.
	all := list(at(sp, "components", "schemas", "TopEntry")["allOf"])
	if len(all) != 2 || obj(all[0])["$ref"] != "#/components/schemas/Player" {
		t.Fatalf("TopEntry must be allOf [Player, {rank}]")
	}
	checkStruct(t, reflect.TypeFor[topJSON](), "TopEntry", obj(all[1]))
}

// checkStruct compares a struct's JSON fields with a schema: same names, the
// same JSON type, a pointer without omitempty is nullable, and omitempty
// fields are the ones not required. Embedded structs are checked on their own.
func checkStruct(t *testing.T, typ reflect.Type, name string, schema map[string]any) {
	t.Helper()
	props := at(schema, "properties")
	var required []string
	for _, r := range list(schema["required"]) {
		required = append(required, r.(string))
	}
	seen := map[string]bool{}
	for i := range typ.NumField() {
		f := typ.Field(i)
		if f.Anonymous {
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")
		field, omitempty := tag[0], slices.Contains(tag[1:], "omitempty")
		seen[field] = true
		p, ok := props[field]
		if !ok {
			t.Errorf("%s.%s: in the code, not in the spec", name, field)
			continue
		}
		if omitempty == slices.Contains(required, field) {
			t.Errorf("%s.%s: omitempty=%v but required=%v", name, field, omitempty, !omitempty)
		}
		ft, nullable := f.Type, false
		if ft.Kind() == reflect.Pointer {
			ft, nullable = ft.Elem(), !omitempty
		}
		types, refs := schemaTypes(obj(p))
		if slices.Contains(types, "null") != nullable {
			t.Errorf("%s.%s: nullable in code %v, in spec types %v", name, field, nullable, types)
		}
		if want := jsonType(ft); want == "$ref" {
			if !slices.Contains(refs, "#/components/schemas/"+schemaOf[ft]) {
				t.Errorf("%s.%s: want a $ref to %s, spec has %v", name, field, schemaOf[ft], refs)
			}
		} else if !slices.Contains(types, want) {
			t.Errorf("%s.%s: Go %s is %s in JSON, spec types %v", name, field, ft, want, types)
		} else if ft == reflect.TypeFor[time.Time]() && obj(p)["format"] != "date-time" {
			t.Errorf("%s.%s: a time needs format date-time", name, field)
		} else if want == "array" {
			if items, _ := schemaTypes(obj(obj(p)["items"])); !slices.Contains(items, jsonType(ft.Elem())) {
				t.Errorf("%s.%s: items %v, want %s", name, field, items, jsonType(ft.Elem()))
			}
		}
	}
	for field := range props {
		if !seen[field] {
			t.Errorf("%s.%s: in the spec, not in the code", name, field)
		}
	}
}

// schemaTypes lists a property's JSON types and $refs, looking into oneOf.
func schemaTypes(p map[string]any) (types, refs []string) {
	alts := []any{p}
	if one := list(p["oneOf"]); one != nil {
		alts = one
	}
	for _, a := range alts {
		m := obj(a)
		switch v := m["type"].(type) {
		case string:
			types = append(types, v)
		case []any:
			for _, x := range v {
				types = append(types, x.(string))
			}
		}
		if r, ok := m["$ref"].(string); ok {
			refs = append(refs, r)
		}
	}
	return types, refs
}

func jsonType(t reflect.Type) string {
	switch {
	case t == reflect.TypeFor[time.Time]():
		return "string"
	case t.Kind() == reflect.Struct:
		return "$ref"
	case t.Kind() == reflect.Slice:
		return "array"
	case t.Kind() == reflect.Bool:
		return "boolean"
	case t.Kind() == reflect.String:
		return "string"
	case t.Kind() >= reflect.Int && t.Kind() <= reflect.Uint64:
		return "integer"
	}
	return t.Kind().String()
}

// TestOpenAPIParamsMatchHandlers sends every documented value of every
// parameter, and values just outside, through the real handlers.
func TestOpenAPIParamsMatchHandlers(t *testing.T) {
	sp := loadSpec(t)
	_, fp := playersServer()
	keys := newFakeKeys(map[string]*dao.ApiKey{goodKey: {Id: 1, Name: "test", RatePerMin: 1_000_000}})
	srv := newServer("/api", nil, newAuth(keys, time.Minute, 600), fp, zap.NewNop().Sugar())

	valid := map[string]string{"q": "stal", "fideid": "1786644"}
	for path, ops := range at(sp, "paths") {
		var params []map[string]any
		for _, p := range list(at(ops, "get")["parameters"]) {
			params = append(params, deref(sp, obj(p)))
		}
		// status sends the request with one parameter set to v ("" leaves it out).
		status := func(name, v string) int {
			pth, q := path, url.Values{}
			for _, p := range params {
				n, val := p["name"].(string), valid[p["name"].(string)]
				if n == name {
					val = v
				}
				if p["in"] == "path" {
					pth = strings.ReplaceAll(pth, "{"+n+"}", val)
				} else if val != "" {
					q.Set(n, val)
				}
			}
			return errCode(t, get(srv, "/api"+pth+"?"+q.Encode(), goodKey))
		}
		for _, p := range params {
			name, sch := p["name"].(string), obj(p["schema"])
			ok := func(v string) {
				if c := status(name, v); c == 400 {
					t.Errorf("%s: %s=%q is documented but rejected", path, name, v)
				}
			}
			bad := func(v string) {
				if c := status(name, v); c != 400 {
					t.Errorf("%s: %s=%q should be a 400, got %d", path, name, v, c)
				}
			}
			if p["required"] == true && p["in"] == "query" {
				bad("")
			}
			for _, e := range list(sch["enum"]) {
				ok(e.(string))
			}
			if sch["enum"] != nil {
				bad("zzz")
			}
			if sch["type"] == "integer" {
				bad("x")
				if lo, has := sch["minimum"].(float64); has {
					ok(strconv.Itoa(int(lo)))
					bad(strconv.Itoa(int(lo) - 1))
				}
				if hi, has := sch["maximum"].(float64); has {
					ok(strconv.Itoa(int(hi)))
					bad(strconv.Itoa(int(hi) + 1))
				}
			}
			if sch["pattern"] == "^[A-Za-z]{3}$" {
				ok("SWE")
				ok("swe")
				bad("SW")
				bad("SWE1")
			}
		}
	}
}
