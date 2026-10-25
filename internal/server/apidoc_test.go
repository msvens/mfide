package server

import (
	"os"
	"strings"
	"testing"
)

// TestAPIGuideCoversSpec keeps API.md (the guide) from falling behind
// api/openapi.json (the contract): every path and query parameter must at
// least be mentioned. It can't check the prose, so API.md avoids exact limits
// and field lists, which live only in the spec.
func TestAPIGuideCoversSpec(t *testing.T) {
	b, err := os.ReadFile("../../API.md")
	if err != nil {
		t.Fatal(err)
	}
	guide := string(b)
	sp := loadSpec(t)
	for path, ops := range at(sp, "paths") {
		if !strings.Contains(guide, "GET "+path) {
			t.Errorf("API.md doesn't mention GET %s", path)
		}
		for _, p := range list(at(ops, "get")["parameters"]) {
			p := deref(sp, obj(p))
			if p["in"] == "query" && !strings.Contains(guide, "`"+p["name"].(string)+"`") {
				t.Errorf("API.md doesn't mention the %s parameter `%s`", path, p["name"])
			}
		}
	}
}
