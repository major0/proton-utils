package cli

import (
	"regexp"
	"testing"
)

// TestAppVersionFormat verifies that AppVersion matches the Proton API format:
// <platform>-<product>@<semver> (with optional pre-release and build metadata).
func TestAppVersionFormat(t *testing.T) {
	// Pattern: word-word@semver (with optional pre-release +build)
	pattern := `^[a-z]+-[a-z]+@\d+\.\d+\.\d+`
	re := regexp.MustCompile(pattern)

	if !re.MatchString(AppVersion) {
		t.Errorf("AppVersion %q does not match pattern %q", AppVersion, pattern)
	}
}
