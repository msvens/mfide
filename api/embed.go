// Package api holds the OpenAPI document mfide serves at /api/openapi.json.
package api

import (
	_ "embed"
	"encoding/json"
)

// OpenAPI is openapi.json as written in the repo; it's served byte for byte so
// clients diffing it see exactly the repo file.
//
//go:embed openapi.json
var OpenAPI []byte

// Version is the document's info.version: the release in which the API last
// changed.
func Version() string {
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(OpenAPI, &doc); err != nil {
		return "unknown"
	}
	return doc.Info.Version
}
