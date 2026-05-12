package driveCmd

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// preserveEntry tracks metadata to apply after copy completes.
type preserveEntry struct {
	dstPath string
	mode    os.FileMode
	mtime   time.Time
}

// preserveFlags holds parsed --preserve flag values.
type preserveFlags struct {
	mode       bool
	timestamps bool
}

// applyPreserve applies preserved mode and mtime to destination files.
func applyPreserve(entries []preserveEntry, opts cpOptions) {
	preserve := parsePreserve(opts)
	if !preserve.mode && !preserve.timestamps {
		return
	}
	for _, e := range entries {
		if preserve.mode {
			if err := os.Chmod(e.dstPath, e.mode); err != nil {
				fmt.Fprintf(os.Stderr, "cp: preserve mode %s: %v\n", e.dstPath, err)
			}
		}
		if preserve.timestamps {
			if err := os.Chtimes(e.dstPath, e.mtime, e.mtime); err != nil {
				fmt.Fprintf(os.Stderr, "cp: preserve timestamps %s: %v\n", e.dstPath, err)
			}
		}
	}
}

// parsePreserve parses the --preserve flag value from opts.
func parsePreserve(opts cpOptions) preserveFlags {
	var pf preserveFlags
	for _, s := range strings.Split(opts.preserve, ",") {
		switch strings.TrimSpace(s) {
		case "mode":
			pf.mode = true
		case "timestamps":
			pf.timestamps = true
		}
	}
	return pf
}
