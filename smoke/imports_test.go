package smoke

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoPflagDirectImports scans all .go files in the project and fails if any
// directly import "github.com/spf13/pflag". The project uses a go.mod replace
// directive to redirect spf13/pflag to optargs/pflag for transitive deps (cobra),
// but no project source should reference spf13/pflag directly — except the flag
// implementation layer (internal/cli/flags.go) which wraps pflag types.
//
// Validates: Requirements 1.2
func TestNoPflagDirectImports(t *testing.T) {
	// Walk the parent directory (project root).
	root := ".."
	root, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	const banned = "github.com/spf13/pflag"
	fset := token.NewFileSet()

	// allowedFiles lists files that legitimately import pflag because they
	// implement the custom flag type wrappers (BoolFunc, BoolFuncP, etc.)
	// or use pflag directly for CLI flag parsing (the daemon binary).
	allowedFiles := map[string]bool{
		filepath.Join("internal", "cli", "flags.go"):      true,
		filepath.Join("internal", "cli", "flags_test.go"): true,
		filepath.Join("cmd", "proton-fuse", "main.go"):    true,
	}

	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// Skip hidden dirs (.git), vendor, and this smoke test directory.
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") || name == "vendor" || name == "smoke" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") {
			return nil
		}

		rel, _ := filepath.Rel(root, path)
		if allowedFiles[rel] {
			return nil
		}

		f, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Logf("skipping %s: %v", path, parseErr)
			return nil
		}

		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if importPath == banned {
				t.Errorf("%s imports %q — use github.com/major0/optargs/pflag instead", rel, banned)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
