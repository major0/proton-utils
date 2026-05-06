package config

// ParamSource indicates where a Param's value originated.
type ParamSource int

const (
	// Unset means the Param has not been configured; the compiled default applies.
	Unset ParamSource = iota
	// File means the value was loaded from the config file.
	File
	// CLI means the value was set via a CLI flag for this invocation.
	CLI
)

// String returns the human-readable name of the source.
func (s ParamSource) String() string {
	switch s {
	case Unset:
		return "default"
	case File:
		return "file"
	case CLI:
		return "cli"
	default:
		return "unknown"
	}
}

// Param holds a typed config value with provenance tracking.
// The zero value is not useful; use NewParam to construct instances.
type Param[T any] struct {
	value  T
	dflt   T
	source ParamSource
}

// NewParam creates a Param with the given default and source Unset.
func NewParam[T any](dflt T) Param[T] {
	return Param[T]{dflt: dflt, source: Unset}
}

// Value returns the effective value: the set value if source != Unset,
// otherwise the compiled default.
func (p Param[T]) Value() T {
	if p.source == Unset {
		return p.dflt
	}
	return p.value
}

// Default returns the compiled default.
func (p Param[T]) Default() T {
	return p.dflt
}

// IsSet returns true when source is File or CLI.
func (p Param[T]) IsSet() bool {
	return p.source != Unset
}

// Source returns the provenance indicator.
func (p Param[T]) Source() ParamSource {
	return p.source
}

// SetFile sets the value with source File.
func (p *Param[T]) SetFile(v T) {
	p.value = v
	p.source = File
}

// SetCLI sets the value with source CLI (highest precedence).
func (p *Param[T]) SetCLI(v T) {
	p.value = v
	p.source = CLI
}

// Reset reverts the Param to Unset (compiled default).
func (p *Param[T]) Reset() {
	var zero T
	p.value = zero
	p.source = Unset
}

// ParamInfo is a type-erased view of a Param for display purposes.
type ParamInfo struct {
	Value   string
	Default string
	Source  ParamSource
}

// Info returns a type-erased view for display, using the provided formatter.
func (p Param[T]) Info(format func(any) string) ParamInfo {
	return ParamInfo{
		Value:   format(p.Value()),
		Default: format(p.dflt),
		Source:  p.source,
	}
}
