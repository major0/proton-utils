package driveCmd

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var findFlags struct {
	name     string
	iname    string
	findType string
	minSize  int64
	maxSize  int64
	mtime    int
	newer    string
	maxDepth int
	print0   bool
	print    bool // -print is default behavior, explicit flag for compatibility
	depth    bool // -depth: process directory contents before the directory itself
	trashed  bool // include trashed items
}

var driveFindCmd = &cobra.Command{
	Use:   "find [options] [<path>]",
	Short: "Search for files and directories in Proton Drive",
	Long:  "Search for files and directories in Proton Drive, compatible with Unix find",
	RunE:  runFind,
}

func init() {
	driveCmd.AddCommand(driveFindCmd)
	f := driveFindCmd.Flags()
	f.SetLongOnly(true)
	f.StringVar(&findFlags.name, "name", "", "Match file name (glob pattern, case-sensitive)")
	f.StringVar(&findFlags.iname, "iname", "", "Match file name (glob pattern, case-insensitive)")
	f.StringVar(&findFlags.findType, "type", "", "File type: f (file), d (directory)")
	f.Int64Var(&findFlags.minSize, "minsize", 0, "Minimum file size in bytes")
	f.Int64Var(&findFlags.maxSize, "maxsize", 0, "Maximum file size in bytes")
	f.IntVar(&findFlags.mtime, "mtime", 0, "Modified time in days (negative=within N days, positive=older than N days)")
	f.StringVar(&findFlags.newer, "newer", "", "Match files newer than this ISO date (YYYY-MM-DD)")
	f.IntVar(&findFlags.maxDepth, "maxdepth", -1, "Maximum directory depth (-1 for unlimited)")
	cli.BoolFlag(f, &findFlags.print0, "print0", false, "Separate output with NUL instead of newline")
	cli.BoolFlag(f, &findFlags.print, "print", false, "Print matching paths (default action)")
	cli.BoolFlag(f, &findFlags.depth, "depth", false, "Process directory contents before the directory itself")
	cli.BoolFlag(f, &findFlags.trashed, "trashed", false, "Include trashed items in results")
}

type findPredicate func(p string, l *drive.Link, depth int, entryName string) bool

func buildPredicates() []findPredicate {
	var preds []findPredicate

	if findFlags.findType != "" {
		preds = append(preds, func(_ string, l *drive.Link, _ int, _ string) bool {
			switch findFlags.findType {
			case "f":
				return l.Type() == proton.LinkTypeFile
			case "d":
				return l.Type() == proton.LinkTypeFolder
			default:
				return true
			}
		})
	}

	if findFlags.name != "" {
		preds = append(preds, func(_ string, _ *drive.Link, _ int, entryName string) bool {
			matched, _ := path.Match(findFlags.name, entryName)
			return matched
		})
	}

	if findFlags.iname != "" {
		pattern := strings.ToLower(findFlags.iname)
		preds = append(preds, func(_ string, _ *drive.Link, _ int, entryName string) bool {
			matched, _ := path.Match(pattern, strings.ToLower(entryName))
			return matched
		})
	}

	if findFlags.minSize > 0 {
		preds = append(preds, func(_ string, l *drive.Link, _ int, _ string) bool {
			return l.Size() >= findFlags.minSize
		})
	}

	if findFlags.maxSize > 0 {
		preds = append(preds, func(_ string, l *drive.Link, _ int, _ string) bool {
			return l.Size() <= findFlags.maxSize
		})
	}

	if findFlags.mtime != 0 {
		preds = append(preds, func(_ string, l *drive.Link, _ int, _ string) bool {
			mt := time.Unix(l.ModifyTime(), 0)
			days := time.Since(mt).Hours() / 24
			if findFlags.mtime < 0 {
				return days <= float64(-findFlags.mtime)
			}
			return days >= float64(findFlags.mtime)
		})
	}

	if findFlags.newer != "" {
		t, err := time.Parse("2006-01-02", findFlags.newer)
		if err == nil {
			preds = append(preds, func(_ string, l *drive.Link, _ int, _ string) bool {
				return time.Unix(l.ModifyTime(), 0).After(t)
			})
		}
	}

	return preds
}

func matchAll(preds []findPredicate, p string, l *drive.Link, depth int, entryName string) bool {
	for _, pred := range preds {
		if !pred(p, l, depth, entryName) {
			return false
		}
	}
	return true
}

func runFind(_ *cobra.Command, args []string) error {
	ctx := context.Background()

	session, err := cli.RestoreSession(ctx)
	if err != nil {
		return err
	}

	dc, err := cli.NewDriveClient(ctx, session)
	if err != nil {
		return err
	}

	// No args → search root share. Explicit paths → search those.
	var roots []*drive.Link
	var rootPaths []string

	if len(args) == 0 {
		// Default to root share (main volume share).
		share, err := dc.ResolveShareByType(ctx, proton.ShareTypeMain)
		if err != nil {
			return fmt.Errorf("find: resolving root share: %w", err)
		}
		name, _ := share.GetName(ctx)
		roots = append(roots, share.Link)
		rootPaths = append(rootPaths, name+"/")
	} else {
		for _, arg := range args {
			link, _, err := ResolveProtonPath(ctx, dc, arg)
			if err != nil {
				return fmt.Errorf("find: %s: %w", arg, err)
			}
			p := strings.TrimSuffix(arg, "/")
			if link.Type() == proton.LinkTypeFolder {
				p += "/"
			}
			roots = append(roots, link)
			rootPaths = append(rootPaths, p)
		}
	}

	order := drive.BreadthFirst
	if findFlags.depth {
		order = drive.DepthFirst
	}

	sep := "\n"
	if findFlags.print0 {
		sep = "\x00"
	}

	preds := buildPredicates()

	for i, root := range roots {
		results := make(chan drive.WalkEntry, 64)
		var walkErr error

		go func() {
			defer close(results)
			walkErr = dc.TreeWalk(ctx, root, rootPaths[i], order, findFlags.maxDepth, results)
		}()

		for entry := range results {
			// Skip trashed/deleted.
			state := entry.Link.State()
			if state == proton.LinkStateDeleted {
				continue
			}
			if state == proton.LinkStateTrashed && !findFlags.trashed {
				continue
			}

			// Apply maxdepth.
			if findFlags.maxDepth >= 0 && entry.Depth > findFlags.maxDepth {
				continue
			}

			if matchAll(preds, entry.Path, entry.Link, entry.Depth, entry.EntryName) {
				fmt.Print(entry.Path + sep)
			}
		}

		if walkErr != nil {
			return walkErr
		}
	}

	return nil
}
