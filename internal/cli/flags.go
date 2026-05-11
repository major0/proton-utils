package cli

import (
	"strconv"

	"github.com/spf13/pflag"
)

// BoolFlag registers a boolean flag that does NOT require an argument.
// Uses BoolFunc internally so that --flag (without =true) works correctly.
// This works around optargs/pflag's BoolVar having BoolTakesArg()=true.
func BoolFlag(fs *pflag.FlagSet, p *bool, name string, value bool, usage string) {
	*p = value
	fs.BoolFunc(name, usage, func(s string) error {
		if s == "" {
			*p = true
			return nil
		}
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		*p = v
		return nil
	})
}

// BoolFlagP is like BoolFlag but accepts a shorthand letter.
func BoolFlagP(fs *pflag.FlagSet, p *bool, name, shorthand string, value bool, usage string) {
	*p = value
	fs.BoolFuncP(name, shorthand, usage, func(s string) error {
		if s == "" {
			*p = true
			return nil
		}
		v, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		*p = v
		return nil
	})
}
