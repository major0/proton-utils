package lumoCmd

import (
	"fmt"
	"strings"
)

const lumoURIPrefix = "lumo://"

// LumoURI holds the parsed components of a lumo:// URI.
type LumoURI struct {
	Space string // authority component (may be empty)
	Path  string // path component (may be empty)
}

// normalizeArg prepends "lumo:///" to bare strings; returns lumo:// URIs unchanged.
func normalizeArg(arg string) string {
	if strings.HasPrefix(arg, lumoURIPrefix) {
		return arg
	}
	return "lumo:///" + arg
}

// parseLumoURI parses a normalized lumo:// URI into its components.
// It strips the "lumo://" prefix, then splits the remainder on the first "/".
// Left of the split is Space, right is Path. If no "/" is present, Path is empty.
// Returns an error if the URI doesn't start with "lumo://".
func parseLumoURI(uri string) (LumoURI, error) {
	if !strings.HasPrefix(uri, lumoURIPrefix) {
		return LumoURI{}, fmt.Errorf("invalid lumo URI: must start with %q", lumoURIPrefix)
	}

	remainder := strings.TrimPrefix(uri, lumoURIPrefix)

	idx := strings.IndexByte(remainder, '/')
	if idx < 0 {
		return LumoURI{Space: remainder, Path: ""}, nil
	}

	return LumoURI{
		Space: remainder[:idx],
		Path:  remainder[idx+1:],
	}, nil
}
