package cli

import (
	"testing"

	"github.com/spf13/pflag"
)

func TestBoolFlag(t *testing.T) {
	tests := []struct {
		name    string
		defVal  bool
		args    []string
		want    bool
		wantErr bool
	}{
		{"default false, no flag", false, nil, false, false},
		{"default true, no flag", true, nil, true, false},
		{"bare flag sets true", false, []string{"--myflag"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			var got bool
			BoolFlag(fs, &got, "myflag", tt.defVal, "test flag")

			err := fs.Parse(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBoolFlag_Set exercises the ParseBool path via FlagSet.Set (which
// BoolFunc uses internally when a value string is provided).
func TestBoolFlag_Set(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{"set true", "true", true, false},
		{"set false", "false", false, false},
		{"set 1", "1", true, false},
		{"set 0", "0", false, false},
		{"invalid value", "notabool", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			var got bool
			BoolFlag(fs, &got, "myflag", false, "test flag")

			err := fs.Set("myflag", tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBoolFlagP(t *testing.T) {
	tests := []struct {
		name    string
		defVal  bool
		args    []string
		want    bool
		wantErr bool
	}{
		{"default false, no flag", false, nil, false, false},
		{"default true, no flag", true, nil, true, false},
		{"long bare flag", false, []string{"--myflag"}, true, false},
		{"short bare flag", false, []string{"-m"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			var got bool
			BoolFlagP(fs, &got, "myflag", "m", tt.defVal, "test flag")

			err := fs.Parse(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBoolFlagP_Set exercises the ParseBool path via FlagSet.Set.
func TestBoolFlagP_Set(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{"set true", "true", true, false},
		{"set false", "false", false, false},
		{"invalid value", "bad", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
			var got bool
			BoolFlagP(fs, &got, "myflag", "m", false, "test flag")

			err := fs.Set("myflag", tt.value)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
