package v370

import (
	"embed"
	"fmt"
	"path"
	"sort"
	"strings"
)

// Built-in profiles, extracted from the captures in testdata/traces with
// `v370-usbsim profile` (see v370/profiles/README.md).
//
//go:embed profiles/*.json
var profileFS embed.FS

// ProfileNames lists the built-in profiles.
func ProfileNames() []string {
	entries, _ := profileFS.ReadDir("profiles")
	var names []string
	for _, e := range entries {
		if n, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// LoadProfile returns a built-in profile by name.
func LoadProfile(name string) (*Profile, error) {
	for _, n := range ProfileNames() {
		if n == name {
			b, err := profileFS.ReadFile(path.Join("profiles", n+".json"))
			if err != nil {
				return nil, err
			}
			return ParseProfile(b)
		}
	}
	return nil, fmt.Errorf("unknown profile %q (available: %s)", name, strings.Join(ProfileNames(), ", "))
}
