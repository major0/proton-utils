package smoke

import (
	"math/rand"
	"reflect"
	"testing"
	"testing/quick"

	"github.com/spf13/cobra"
)

// flagDef describes a flag registered on a test command.
type flagDef struct {
	Name    string
	IsBool  bool
	Default string
}

// testFlags is the set of long flags we register, chosen to have
// distinct prefixes at various lengths for abbreviation testing.
var testFlags = []flagDef{
	{Name: "verbose", IsBool: false}, // count flag, but string for prefix test
	{Name: "account", IsBool: false},
	{Name: "config-file", IsBool: false},
	{Name: "session-file", IsBool: false},
	{Name: "timeout", IsBool: false},
	{Name: "max-jobs", IsBool: false},
	{Name: "debug", IsBool: true, Default: "false"},
	{Name: "dry-run", IsBool: true, Default: "false"},
	{Name: "force", IsBool: true, Default: "true"},
}

// newPrefixTestCmd creates a command with string and bool flags for
// abbreviation and negation testing.
func newPrefixTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(_ *cobra.Command, _ []string) error {
			return nil
		},
	}
	for _, f := range testFlags {
		if f.IsBool {
			def := f.Default == "true"
			cmd.Flags().Bool(f.Name, def, f.Name)
		} else {
			cmd.Flags().String(f.Name, "", f.Name)
		}
	}
	return cmd
}

// prefixGen generates (flagIndex, prefixLength) pairs for abbreviation tests.
type prefixGen struct{}

func (prefixGen) Generate(r *rand.Rand, _ int) reflect.Value {
	idx := r.Intn(len(testFlags))
	name := testFlags[idx].Name
	// Prefix length: at least 1, at most full length.
	plen := r.Intn(len(name)) + 1
	return reflect.ValueOf([2]int{idx, plen})
}

// isUnambiguous checks whether a prefix uniquely matches exactly one flag.
func isUnambiguous(prefix string) (string, bool) {
	var matches []string
	for _, f := range testFlags {
		if len(prefix) <= len(f.Name) && f.Name[:len(prefix)] == prefix {
			matches = append(matches, f.Name)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return "", false
}

// PropertyAbbreviationMatchingResolvesUnambiguousPrefixes verifies that
// any unambiguous prefix of a long flag resolves to the full flag.
//
// Property 6: Abbreviation matching resolves unambiguous prefixes
// Validates: Requirements 6.1
func TestPropertyAbbreviationMatching(t *testing.T) {
	f := func(pair [2]int) bool {
		idx := pair[0]
		plen := pair[1]
		flag := testFlags[idx]
		prefix := flag.Name[:plen]

		fullName, unambiguous := isUnambiguous(prefix)
		if !unambiguous {
			// Ambiguous prefix — skip (not testable for this property).
			return true
		}

		cmd := newPrefixTestCmd()

		var args []string
		if flag.IsBool {
			args = []string{"--" + prefix}
		} else {
			args = []string{"--" + prefix + "=testval"}
		}
		cmd.SetArgs(args)

		if err := cmd.Execute(); err != nil {
			t.Logf("prefix %q (full: %q) failed: %v", prefix, fullName, err)
			return false
		}

		if flag.IsBool {
			got, err := cmd.Flags().GetBool(fullName)
			if err != nil {
				t.Logf("GetBool(%q): %v", fullName, err)
				return false
			}
			return got == true
		}

		got, err := cmd.Flags().GetString(fullName)
		if err != nil {
			t.Logf("GetString(%q): %v", fullName, err)
			return false
		}
		return got == "testval"
	}

	if err := quick.Check(f, &quick.Config{
		MaxCount: 100,
		Values:   func(args []reflect.Value, r *rand.Rand) { args[0] = prefixGen{}.Generate(r, 0) },
	}); err != nil {
		t.Errorf("Property 6 failed: %v", err)
	}
}

// boolFlagGen generates indices into the boolean flags in testFlags.
type boolFlagGen struct{}

func (boolFlagGen) Generate(r *rand.Rand, _ int) reflect.Value {
	var boolIdxs []int
	for i, f := range testFlags {
		if f.IsBool {
			boolIdxs = append(boolIdxs, i)
		}
	}
	return reflect.ValueOf(boolIdxs[r.Intn(len(boolIdxs))])
}

// PropertyBooleanNegation verifies that --no-<flag> sets a boolean flag
// to false regardless of its default value.
//
// Property 7: Boolean negation
// Validates: Requirements 7.1, 7.2
func TestPropertyBooleanNegation(t *testing.T) {
	f := func(idx int) bool {
		flag := testFlags[idx]
		if !flag.IsBool {
			return true // skip non-bool
		}

		cmd := newPrefixTestCmd()
		cmd.SetArgs([]string{"--no-" + flag.Name})

		if err := cmd.Execute(); err != nil {
			t.Logf("--no-%s failed: %v", flag.Name, err)
			return false
		}

		got, err := cmd.Flags().GetBool(flag.Name)
		if err != nil {
			t.Logf("GetBool(%q): %v", flag.Name, err)
			return false
		}
		return got == false
	}

	if err := quick.Check(f, &quick.Config{
		MaxCount: 100,
		Values:   func(args []reflect.Value, r *rand.Rand) { args[0] = boolFlagGen{}.Generate(r, 0) },
	}); err != nil {
		t.Errorf("Property 7 failed: %v", err)
	}
}
