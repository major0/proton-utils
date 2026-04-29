package driveCmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/docker/go-units"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// outputFormat controls how entries are displayed.
type outputFormat int

const (
	formatColumns outputFormat = iota
	formatLong
	formatSingle
	formatAcross
)

// sortMode controls how entries are ordered.
type sortMode int

const (
	sortName sortMode = iota
	sortSize
	sortTime
	sortNone
)

// timeStyle controls time formatting in long mode.
type timeStyle int

const (
	timeDefault timeStyle = iota
	timeFull
	timeISO
	timeLongISO
)

// listEntry pairs a DirEntry with its resolved display name. The name
// is resolved once at collection time via EntryName() and reused for
// filtering, sorting, and display — avoiding repeated decryption.
type listEntry struct {
	entry drive.DirEntry
	name  string // resolved display name
}

type listOpts struct {
	format    outputFormat
	sortBy    sortMode
	timeStyle timeStyle
	human     bool
	all       bool
	almostAll bool
	recursive bool
	reverse   bool
	color     bool
	trash     bool
	classify  bool
	inode     bool
}

var listFlags struct {
	all, almostAll, long, single, across, columns bool
	human, recursive, reverse                     bool
	sortSize, sortTime, unsorted                  bool
	fullTime, trash, classify, inode              bool
	format, sortWord, timeStyle, color            string
}

var driveListCmd = &cobra.Command{
	Use:     "list [options] [<path> ...]",
	Aliases: []string{"ls"},
	Short:   "List files and directories in Proton Drive",
	Long:    "List files and directories in Proton Drive",
	RunE:    runList,
}

func init() {
	driveCmd.AddCommand(driveListCmd)
	f := driveListCmd.Flags()
	cli.BoolFlagP(f, &listFlags.all, "all", "a", false, "Do not ignore entries starting with '.'")
	cli.BoolFlagP(f, &listFlags.almostAll, "almost-all", "A", false, "Do not list implied '.' and '..'")
	cli.BoolFlagP(f, &listFlags.long, "long", "l", false, "Use long listing format")
	cli.BoolFlagP(f, &listFlags.single, "single-column", "1", false, "List one file per line")
	cli.BoolFlagP(f, &listFlags.across, "across", "x", false, "List entries by lines instead of columns")
	cli.BoolFlagP(f, &listFlags.columns, "columns", "C", false, "List entries in columns")
	f.StringVar(&listFlags.format, "format", "", "Output format: long, single-column, across, columns")
	cli.BoolFlag(f, &listFlags.human, "human-readable", false, "Print sizes in human-readable format")
	cli.BoolFlagP(f, &listFlags.sortSize, "sort-size", "S", false, "Sort by file size, largest first")
	cli.BoolFlagP(f, &listFlags.sortTime, "sort-time", "t", false, "Sort by modification time, newest first")
	cli.BoolFlagP(f, &listFlags.unsorted, "unsorted", "U", false, "Do not sort; list in directory order")
	cli.BoolFlagP(f, &listFlags.reverse, "reverse", "r", false, "Reverse sort order")
	f.StringVar(&listFlags.sortWord, "sort", "", "Sort by: name, size, time, none")
	cli.BoolFlag(f, &listFlags.fullTime, "full-time", false, "Like -l --time-style=full-iso")
	f.StringVar(&listFlags.timeStyle, "time-style", "", "Time format: full-iso, long-iso, iso")
	cli.BoolFlagP(f, &listFlags.recursive, "recursive", "R", false, "List subdirectories recursively")
	f.StringVar(&listFlags.color, "color", "auto", "Colorize output: auto, always, never")
	cli.BoolFlag(f, &listFlags.trash, "trash", false, "Show only trashed items")
	cli.BoolFlagP(f, &listFlags.classify, "classify", "F", false, "Append indicator (/ for directories) to entries")
	cli.BoolFlagP(f, &listFlags.inode, "inode", "i", false, "Print link ID for each entry")
}

func resolveOpts() (listOpts, error) {
	opts := listOpts{
		all: listFlags.all, almostAll: listFlags.almostAll,
		human: listFlags.human, recursive: listFlags.recursive,
		reverse: listFlags.reverse, trash: listFlags.trash,
		classify: listFlags.classify, inode: listFlags.inode,
	}

	if term.IsTerminal(int(os.Stdout.Fd())) { //nolint:gosec
		opts.format = formatColumns
	} else {
		opts.format = formatSingle
	}

	if listFlags.columns {
		opts.format = formatColumns
	}
	if listFlags.single {
		opts.format = formatSingle
	}
	if listFlags.across {
		opts.format = formatAcross
	}
	if listFlags.long {
		opts.format = formatLong
	}

	switch listFlags.format {
	case "":
	case "long", "verbose":
		opts.format = formatLong
	case "single-column":
		opts.format = formatSingle
	case "across", "horizontal":
		opts.format = formatAcross
	case "columns", "vertical":
		opts.format = formatColumns
	default:
		return opts, fmt.Errorf("invalid --format value: %q", listFlags.format)
	}

	opts.sortBy = sortName
	if listFlags.sortSize {
		opts.sortBy = sortSize
	}
	if listFlags.sortTime {
		opts.sortBy = sortTime
	}
	if listFlags.unsorted {
		opts.sortBy = sortNone
	}

	switch listFlags.sortWord {
	case "":
	case "name":
		opts.sortBy = sortName
	case "size":
		opts.sortBy = sortSize
	case "time":
		opts.sortBy = sortTime
	case "none":
		opts.sortBy = sortNone
	default:
		return opts, fmt.Errorf("invalid --sort value: %q", listFlags.sortWord)
	}

	opts.timeStyle = timeDefault
	switch listFlags.timeStyle {
	case "":
	case "full-iso":
		opts.timeStyle = timeFull
	case "long-iso":
		opts.timeStyle = timeLongISO
	case "iso":
		opts.timeStyle = timeISO
	default:
		return opts, fmt.Errorf("invalid --time-style value: %q", listFlags.timeStyle)
	}

	if listFlags.fullTime {
		opts.format = formatLong
		opts.timeStyle = timeFull
	}

	switch listFlags.color {
	case "always":
		opts.color = true
	case "never":
		opts.color = false
	case "auto", "":
		opts.color = term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec
	default:
		return opts, fmt.Errorf("invalid --color value: %q (use auto, always, or never)", listFlags.color)
	}

	return opts, nil
}

// collectEntries reads directory entries and resolves names once.
// With -a: includes . and .. entries.
// With -A: includes dot-files but not . and ..
// Without -a/-A: skips . and .. (uses ListChildren).
func collectEntries(ctx context.Context, dir *drive.Link, opts listOpts) ([]listEntry, error) {
	if opts.all {
		// -a: include . and ..
		var entries []listEntry
		for de := range dir.Readdir(ctx) {
			if de.Err != nil {
				return nil, de.Err
			}
			name, err := de.EntryName()
			if err != nil {
				return nil, err
			}
			entries = append(entries, listEntry{entry: de, name: name})
		}
		return entries, nil
	}
	// No -a: skip . and ..
	children, err := dir.ListChildren(ctx, true)
	if err != nil {
		return nil, err
	}
	entries := make([]listEntry, 0, len(children))
	for _, l := range children {
		name, err := l.Name()
		if err != nil {
			return nil, err
		}
		entries = append(entries, listEntry{entry: drive.DirEntry{Link: l}, name: name})
	}
	return entries, nil
}

func resolveEntries(ctx context.Context, dc *driveClient.Client, args []string, opts listOpts) ([]listEntry, error) {
	// No args → list root share contents (same as proton:///).
	if len(args) == 0 {
		share, err := dc.ResolveShareByType(ctx, proton.ShareTypeMain)
		if err != nil {
			return nil, fmt.Errorf("resolving root share: %w", err)
		}
		return collectEntries(ctx, share.Link, opts)
	}

	var entries []listEntry
	for _, arg := range args {
		link, _, err := ResolveProtonPath(ctx, dc, arg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", arg, err)
		}

		switch {
		case link.Type() == proton.LinkTypeFolder:
			dirEntries, err := collectEntries(ctx, link, opts)
			if err != nil {
				return nil, err
			}
			entries = append(entries, dirEntries...)

		case opts.all || opts.almostAll:
			// With -a/-A, a file argument may match multiple links
			// with the same name (e.g. an active file and a trashed
			// file). Scan the parent directory for all matches.
			name, err := link.Name()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			parent := link.Parent()
			for de := range parent.Readdir(ctx) {
				if de.Err != nil {
					return nil, de.Err
				}
				entryName, err := de.EntryName()
				if err != nil {
					return nil, err
				}
				if entryName == name {
					entries = append(entries, listEntry{entry: de, name: entryName})
				}
			}

		default:
			name, err := link.Name()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			entries = append(entries, listEntry{entry: drive.DirEntry{Link: link}, name: name})
		}
	}

	return entries, nil
}

func filterEntries(entries []listEntry, opts listOpts) []listEntry {
	var out []listEntry
	for _, e := range entries {
		l := e.entry.Link
		state := l.State()

		// --trash: show only trashed items
		if opts.trash {
			if state != proton.LinkStateTrashed {
				continue
			}
			out = append(out, e)
			continue
		}

		// Always skip permanently deleted
		if state == proton.LinkStateDeleted {
			continue
		}

		// -a / -A: show trashed items alongside active ones
		// Without -a/-A: hide trashed items
		if state == proton.LinkStateTrashed && !opts.all && !opts.almostAll {
			continue
		}

		// Hide dot-files unless -a or -A
		if !opts.all && !opts.almostAll && strings.HasPrefix(e.name, ".") {
			continue
		}

		out = append(out, e)
	}
	return out
}

func sortEntries(entries []listEntry, opts listOpts) {
	if opts.sortBy == sortNone {
		if opts.reverse {
			for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
				entries[i], entries[j] = entries[j], entries[i]
			}
		}
		return
	}

	sort.SliceStable(entries, func(i, j int) bool {
		var less bool
		switch opts.sortBy {
		case sortSize:
			less = entries[i].entry.Link.Size() > entries[j].entry.Link.Size()
		case sortTime:
			less = entries[i].entry.Link.ModifyTime() > entries[j].entry.Link.ModifyTime()
		default:
			less = strings.ToLower(entries[i].name) < strings.ToLower(entries[j].name)
		}
		if opts.reverse {
			return !less
		}
		return less
	})
}

func formatSize(size int64, opts listOpts) string {
	if opts.human {
		return units.HumanSize(float64(size))
	}
	return fmt.Sprintf("%d", size)
}

func formatTimestamp(epoch int64, style timeStyle) string {
	t := time.Unix(epoch, 0)
	switch style {
	case timeFull:
		return t.Format("2006-01-02 15:04:05.000000000 -0700")
	case timeLongISO:
		return t.Format("2006-01-02 15:04")
	case timeISO:
		return t.Format("01-02 15:04")
	default:
		sixMonthsAgo := time.Now().AddDate(0, -6, 0)
		if t.Before(sixMonthsAgo) {
			return t.Format("Jan _2  2006")
		}
		return t.Format("Jan _2 15:04")
	}
}

func typeChar(lt proton.LinkType) byte {
	if lt == proton.LinkTypeFolder {
		return 'd'
	}
	return '-'
}

// ANSI color codes for ls-style output.
const (
	colorReset    = "\033[0m"
	colorBoldBlue = "\033[1;34m" // directories
	colorBoldRed  = "\033[1;31m" // trashed items
)

// colorName returns the display name with optional ANSI color and classify suffix.
func colorName(name string, l *drive.Link, useColor bool, classify bool) string {
	suffix := ""
	if classify && l.Type() == proton.LinkTypeFolder {
		suffix = "/"
	}

	if !useColor {
		return name + suffix
	}
	if l.State() == proton.LinkStateTrashed {
		return colorBoldRed + name + colorReset + suffix
	}
	if l.Type() == proton.LinkTypeFolder {
		return colorBoldBlue + name + colorReset + suffix
	}
	return name + suffix
}

// rawName returns the plain name with optional classify suffix (no color).
func rawName(name string, l *drive.Link, classify bool) string {
	if classify && l.Type() == proton.LinkTypeFolder {
		return name + "/"
	}
	return name
}

func printLong(e listEntry, opts listOpts) {
	l := e.entry.Link
	prefix := ""
	if opts.inode {
		prefix = fmt.Sprintf("%s ", l.LinkID())
	}
	fmt.Printf("%s%c%-9s %8s %s %s\n",
		prefix,
		typeChar(l.Type()),
		"rwxr-xr-x",
		formatSize(l.Size(), opts),
		formatTimestamp(l.ModifyTime(), opts.timeStyle),
		colorName(e.name, l, opts.color, opts.classify),
	)
}

func printEntries(entries []listEntry, opts listOpts) {
	switch opts.format {
	case formatLong:
		for _, e := range entries {
			printLong(e, opts)
		}
	case formatSingle:
		for _, e := range entries {
			prefix := ""
			if opts.inode {
				prefix = fmt.Sprintf("%s ", e.entry.Link.LinkID())
			}
			fmt.Println(prefix + colorName(e.name, e.entry.Link, opts.color, opts.classify))
		}
	case formatColumns:
		printEntryColumns(entries, false, opts)
	case formatAcross:
		printEntryColumns(entries, true, opts)
	}
}

func printEntryColumns(entries []listEntry, across bool, opts listOpts) {
	if len(entries) == 0 {
		return
	}

	type colEntry struct {
		raw     string
		display string
	}

	cols := make([]colEntry, len(entries))
	maxLen := 0
	for i, e := range entries {
		raw := rawName(e.name, e.entry.Link, opts.classify)
		if opts.inode {
			raw = e.entry.Link.LinkID() + " " + raw
		}
		display := colorName(e.name, e.entry.Link, opts.color, opts.classify)
		if opts.inode {
			display = e.entry.Link.LinkID() + " " + display
		}
		cols[i] = colEntry{raw: raw, display: display}
		if len(raw) > maxLen {
			maxLen = len(raw)
		}
	}

	colWidth := maxLen + 2
	termWidth := 80
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 { //nolint:gosec
		termWidth = w
	}

	numCols := termWidth / colWidth
	if numCols < 1 {
		numCols = 1
	}
	numRows := (len(cols) + numCols - 1) / numCols

	for row := 0; row < numRows; row++ {
		for col := 0; col < numCols; col++ {
			var idx int
			if across {
				idx = row*numCols + col
			} else {
				idx = col*numRows + row
			}
			if idx >= len(cols) {
				continue
			}
			ce := cols[idx]
			if col < numCols-1 {
				padding := colWidth - len(ce.raw)
				if padding < 0 {
					padding = 0
				}
				fmt.Print(ce.display)
				for i := 0; i < padding; i++ {
					fmt.Print(" ")
				}
			} else {
				fmt.Print(ce.display)
			}
		}
		fmt.Println()
	}
}

func listRecursive(ctx context.Context, prefix string, entries []listEntry, opts listOpts) error {
	for _, e := range entries {
		l := e.entry.Link
		if l.Type() != proton.LinkTypeFolder {
			continue
		}

		path := prefix + e.name + "/"
		children, err := collectEntries(ctx, l, opts)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		children = filterEntries(children, opts)
		sortEntries(children, opts)

		fmt.Printf("\n%s:\n", prefix+e.name)
		printEntries(children, opts)

		if err := listRecursive(ctx, path, children, opts); err != nil {
			return err
		}
	}
	return nil
}

func runList(cmd *cobra.Command, args []string) error {
	opts, err := resolveOpts()
	if err != nil {
		return err
	}

	rc := cli.GetContext(cmd)
	ctx, cancel := context.WithTimeout(context.Background(), rc.Timeout)
	defer cancel()

	session, err := cli.RestoreSession(ctx)
	if err != nil {
		return err
	}

	dc, err := driveClient.NewClient(ctx, session)
	if err != nil {
		return err
	}

	slog.Debug("drive.list", "args", args)

	entries, err := resolveEntries(ctx, dc, args, opts)
	if err != nil {
		return err
	}

	entries = filterEntries(entries, opts)
	sortEntries(entries, opts)
	printEntries(entries, opts)

	if opts.recursive {
		if err := listRecursive(ctx, "", entries, opts); err != nil {
			return err
		}
	}

	return nil
}
