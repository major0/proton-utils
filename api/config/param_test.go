package config

import (
	"fmt"
	"testing"

	"pgregory.net/rapid"
)

// TestParamPrecedence_Property verifies that Param[T] respects the
// source precedence chain: CLI > File > Default.
//
// **Property 3: Param precedence**
// **Validates: Requirements 3.2, 3.3, 3.4, 11.3**
func TestParamPrecedence_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := rapid.Int().Draw(t, "default")
		f := rapid.Int().Draw(t, "file")
		c := rapid.Int().Draw(t, "cli")

		p := NewParam(d)

		// Unset: Value() returns default.
		if p.Value() != d {
			t.Fatalf("Unset: Value()=%d, want %d", p.Value(), d)
		}
		if p.Source() != Unset {
			t.Fatalf("Unset: Source()=%v, want Unset", p.Source())
		}
		if p.IsSet() {
			t.Fatal("Unset: IsSet() should be false")
		}

		// SetFile: Value() returns file value.
		p.SetFile(f)
		if p.Value() != f {
			t.Fatalf("File: Value()=%d, want %d", p.Value(), f)
		}
		if p.Source() != File {
			t.Fatalf("File: Source()=%v, want File", p.Source())
		}
		if !p.IsSet() {
			t.Fatal("File: IsSet() should be true")
		}

		// SetCLI: Value() returns CLI value (overrides file).
		p.SetCLI(c)
		if p.Value() != c {
			t.Fatalf("CLI: Value()=%d, want %d", p.Value(), c)
		}
		if p.Source() != CLI {
			t.Fatalf("CLI: Source()=%v, want CLI", p.Source())
		}
		if !p.IsSet() {
			t.Fatal("CLI: IsSet() should be true")
		}

		// Reset: reverts to default.
		p.Reset()
		if p.Value() != d {
			t.Fatalf("Reset: Value()=%d, want %d", p.Value(), d)
		}
		if p.Source() != Unset {
			t.Fatalf("Reset: Source()=%v, want Unset", p.Source())
		}
		if p.IsSet() {
			t.Fatal("Reset: IsSet() should be false")
		}

		// Default is always preserved.
		if p.Default() != d {
			t.Fatalf("Default()=%d, want %d", p.Default(), d)
		}
	})
}

// TestParamPrecedenceString_Property verifies Param precedence with
// string type for generics coverage.
//
// **Property 3: Param precedence (string variant)**
// **Validates: Requirements 3.2, 3.3, 3.4, 11.3**
func TestParamPrecedenceString_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		d := rapid.String().Draw(t, "default")
		f := rapid.String().Draw(t, "file")
		c := rapid.String().Draw(t, "cli")

		p := NewParam(d)

		if p.Value() != d {
			t.Fatalf("Unset: Value()=%q, want %q", p.Value(), d)
		}
		if p.Source() != Unset {
			t.Fatalf("Unset: Source()=%v, want Unset", p.Source())
		}
		if p.IsSet() {
			t.Fatal("Unset: IsSet() should be false")
		}

		p.SetFile(f)
		if p.Value() != f {
			t.Fatalf("File: Value()=%q, want %q", p.Value(), f)
		}
		if p.Source() != File {
			t.Fatalf("File: Source()=%v, want File", p.Source())
		}
		if !p.IsSet() {
			t.Fatal("File: IsSet() should be true")
		}

		p.SetCLI(c)
		if p.Value() != c {
			t.Fatalf("CLI: Value()=%q, want %q", p.Value(), c)
		}
		if p.Source() != CLI {
			t.Fatalf("CLI: Source()=%v, want CLI", p.Source())
		}
		if !p.IsSet() {
			t.Fatal("CLI: IsSet() should be true")
		}

		p.Reset()
		if p.Value() != d {
			t.Fatalf("Reset: Value()=%q, want %q", p.Value(), d)
		}
		if p.Source() != Unset {
			t.Fatalf("Reset: Source()=%v, want Unset", p.Source())
		}
		if p.IsSet() {
			t.Fatal("Reset: IsSet() should be false")
		}

		if p.Default() != d {
			t.Fatalf("Default()=%q, want %q", p.Default(), d)
		}
	})
}

// TestParamInfo verifies the Info method produces correct type-erased output.
func TestParamInfo(t *testing.T) {
	format := func(v any) string { return fmt.Sprintf("%v", v) }

	p := NewParam(42)
	info := p.Info(format)
	if info.Value != "42" {
		t.Fatalf("Unset Info.Value=%q, want %q", info.Value, "42")
	}
	if info.Default != "42" {
		t.Fatalf("Unset Info.Default=%q, want %q", info.Default, "42")
	}
	if info.Source != Unset {
		t.Fatalf("Unset Info.Source=%v, want Unset", info.Source)
	}

	p.SetFile(99)
	info = p.Info(format)
	if info.Value != "99" {
		t.Fatalf("File Info.Value=%q, want %q", info.Value, "99")
	}
	if info.Default != "42" {
		t.Fatalf("File Info.Default=%q, want %q", info.Default, "42")
	}
	if info.Source != File {
		t.Fatalf("File Info.Source=%v, want File", info.Source)
	}
}

// TestParamSourceString verifies the String() method on ParamSource.
func TestParamSourceString(t *testing.T) {
	tests := []struct {
		source ParamSource
		want   string
	}{
		{Unset, "default"},
		{File, "file"},
		{CLI, "cli"},
		{ParamSource(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.source.String(); got != tt.want {
			t.Errorf("ParamSource(%d).String()=%q, want %q", tt.source, got, tt.want)
		}
	}
}
