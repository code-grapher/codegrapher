// Package pkgjson parses package.json files into a typed structure. It is the
// single package.json parser shared by the scope, mcp, and extract packages,
// wrapping encoding/json.
package pkgjson

import "encoding/json"

// File is the parsed content of a package.json (only the fields we index).
type File struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Engines              map[string]string `json:"engines"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
}

// Parse parses package.json content.
func Parse(data []byte) (*File, error) {
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}
