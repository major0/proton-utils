package driveCmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	driveClient "github.com/major0/proton-cli/api/drive/client"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

var cpFlags struct {
	recursive   bool   // -r, -R, --recursive
	archive     bool   // -a (implies -r -d --preserve=mode,timestamps)
	dereference bool   // -L, --dereference (follow symlinks)
	noDeref     bool   // -d (skip symlinks; implied by -a)
	verbose     bool   // -v, --verbose
	progress    bool   // --progress
	preserve    string // --preserve=mode,timestamps
	workers     int    // --workers (override default 8)
	targetDir   string // -t, --target-directory
	removeDest  bool   // --remove-destination (trash Proton / remove local before copy)
	backup      bool   // --backup (local: rename to <name>~; Proton: no-op)
}

var driveCpCmd = &cobra.Command{
	Use:   "cp [options] <source> [<source> ...] <dest>",
	Short: "Copy files and directories",
	Long: `Copy files and directories between local filesystem and Proton Drive,
within Proton Drive, or locally. Supports all four directions:
local→local, local→remote, remote→local, remote→remote.

Proton Drive files are versioned by default — copying over an existing
file creates a new revision preserving the old content.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCp,
}

func init() {
	driveCmd.AddCommand(driveCpCmd)
	f := driveCpCmd.Flags()

	cli.BoolFlagP(f, &cpFlags.recursive, "recursive", "r", false, "Copy directories recursively")
	f.BoolVarP(&cpFlags.recursive, "Recursive", "R", false, "Copy directories recursively (alias for -r)")
	cli.BoolFlagP(f, &cpFlags.archive, "archive", "a", false, "Archive mode: -r -d --preserve=mode,timestamps")
	cli.BoolFlagP(f, &cpFlags.dereference, "dereference", "L", false, "Follow symbolic links")
	cli.BoolFlagP(f, &cpFlags.noDeref, "no-dereference", "d", false, "Skip symbolic links (default; explicit for -a)")
	cli.BoolFlag(f, &cpFlags.verbose, "verbose", false, "Print each file as it completes")
	cli.BoolFlag(f, &cpFlags.progress, "progress", false, "Show aggregate transfer progress")
	f.StringVar(&cpFlags.preserve, "preserve", "", "Preserve attributes: mode,timestamps")
	f.IntVar(&cpFlags.workers, "workers", 0, "Number of concurrent workers (default 8)")
	f.StringVarP(&cpFlags.targetDir, "target-directory", "t", "", "Copy all sources into this directory")
	cli.BoolFlag(f, &cpFlags.removeDest, "remove-destination", false, "Trash/remove destination before copy (disables versioning)")
	cli.BoolFlag(f, &cpFlags.backup, "backup", false, "Backup existing local files as <name>~")
}

func runCp(_ *cobra.Command, args []string) error {
	// Validate mutually exclusive flags.
	if cpFlags.removeDest && cpFlags.backup {
		return fmt.Errorf("cp: --remove-destination and --backup are mutually exclusive")
	}

	// Expand -a into its component flags.
	if cpFlags.archive {
		cpFlags.recursive = true
		cpFlags.noDeref = true
		if cpFlags.preserve == "" {
			cpFlags.preserve = "mode,timestamps"
		}
	}

	// Construct cpOptions from cpFlags — all sub-functions read from
	// opts, not cpFlags.
	opts := cpOptions{
		recursive:   cpFlags.recursive,
		dereference: cpFlags.dereference,
		removeDest:  cpFlags.removeDest,
		backup:      cpFlags.backup,
		preserve:    cpFlags.preserve,
		workers:     cpFlags.workers,
		verbose:     cpFlags.verbose,
		progress:    cpFlags.progress,
	}

	// Validate argument count.
	if cpFlags.targetDir == "" && len(args) < 2 {
		return fmt.Errorf("cp: missing destination operand after %q", args[0]) //nolint:gosec // cobra.MinimumNArgs(1) guarantees len(args) >= 1
	}
	if cpFlags.targetDir != "" && len(args) < 1 {
		return fmt.Errorf("cp: missing source operand")
	}

	// Split args into sources and dest.
	var sources []pathArg
	var dest pathArg

	if cpFlags.targetDir != "" {
		// -t mode: all positional args are sources, -t value is dest.
		dest = pathArg{raw: cpFlags.targetDir, pathType: classifyPath(cpFlags.targetDir)}
		for _, a := range args {
			sources = append(sources, pathArg{raw: a, pathType: classifyPath(a)})
		}
	} else {
		// Default: last arg is dest, rest are sources.
		dest = pathArg{raw: args[len(args)-1], pathType: classifyPath(args[len(args)-1])}
		for _, a := range args[:len(args)-1] {
			sources = append(sources, pathArg{raw: a, pathType: classifyPath(a)})
		}
	}

	// Determine if any path is a Proton path — session setup is only
	// needed when at least one endpoint is remote.
	needSession := dest.pathType == PathProton
	if !needSession {
		for _, s := range sources {
			if s.pathType == PathProton {
				needSession = true
				break
			}
		}
	}

	// Create context — the global timeout applies to session setup, not
	// the bulk transfer which can run for minutes on large files.
	setupCtx, setupCancel := context.WithTimeout(context.Background(), cli.Timeout)
	defer setupCancel()

	var dc *driveClient.Client
	if needSession {
		session, err := cli.RestoreSession(setupCtx)
		if err != nil {
			return err
		}

		dc, err = driveClient.NewClient(setupCtx, session)
		if err != nil {
			return err
		}
	}

	// Transfer context has no timeout — individual API calls have their
	// own timeouts. Ctrl+C cancels via signal handling.
	ctx := context.Background()

	// Resolve destination.
	dstEp, err := resolveDest(ctx, dc, dest, len(sources) > 1)
	if err != nil {
		return err
	}

	// Build CopyJobs for all source/dest pairs.
	var jobs []CopyJob
	var preserves []preserveEntry
	for _, src := range sources {
		srcEp, err := resolveSource(ctx, dc, src, opts)
		if err != nil {
			if errors.Is(err, errSkipSymlink) {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				continue
			}
			return err
		}

		// Compute the effective destination for this source.
		fileDst := dstEp
		if dstEp.isDir() {
			fileDst = &resolvedEndpoint{
				pathType:  dstEp.pathType,
				raw:       dstEp.raw,
				localPath: filepath.Join(dstEp.localPath, srcEp.basename()),
				localInfo: nil,
				link:      dstEp.link,
				share:     dstEp.share,
			}
		}

		// Directory sources: expand recursively or skip.
		if srcEp.isDir() {
			if !opts.recursive {
				fmt.Fprintf(os.Stderr, "cp: %s: is a directory (use -r to copy recursively)\n", srcEp.raw)
				continue
			}
			expanded, preserveExpanded, err := expandRecursive(ctx, dc, srcEp, fileDst, opts)
			if err != nil {
				return err
			}
			jobs = append(jobs, expanded...)
			preserves = append(preserves, preserveExpanded...)
			continue
		}

		if err := handleConflict(ctx, dc, fileDst, opts.removeDest, opts.backup); err != nil {
			return err
		}

		job, err := buildCopyJob(ctx, dc, srcEp, fileDst)
		if err != nil {
			return err
		}
		jobs = append(jobs, *job)

		// Collect preservation metadata for local destinations.
		if fileDst.pathType == PathLocal && srcEp.pathType == PathLocal && srcEp.localInfo != nil {
			preserves = append(preserves, preserveEntry{
				dstPath: fileDst.localPath,
				mode:    srcEp.localInfo.Mode().Perm(),
				mtime:   srcEp.localInfo.ModTime(),
			})
		}
	}

	if len(jobs) == 0 {
		return nil
	}

	if err := RunPipeline(ctx, jobs, transferOpts(opts)); err != nil {
		return err
	}

	// Apply preserved attributes after all blocks are written.
	applyPreserve(preserves, opts)
	return nil
}

// buildCopyJob constructs a CopyJob from resolved source and destination
// endpoints. For Proton endpoints, uses CreateFile/OpenFile to get the
// FileHandle with revision, session key, and block info.
func buildCopyJob(ctx context.Context, dc *driveClient.Client, src, dst *resolvedEndpoint) (*CopyJob, error) {
	// Check for same source and destination.
	if src.pathType == PathLocal && dst.pathType == PathLocal && src.localPath == dst.localPath {
		return nil, fmt.Errorf("cp: %s: source and destination are the same", src.raw)
	}
	if src.pathType == PathProton && dst.pathType == PathProton &&
		src.link != nil && dst.link != nil && src.link.LinkID() == dst.link.LinkID() {
		return nil, fmt.Errorf("cp: %s: source and destination are the same", src.raw)
	}

	var job CopyJob

	// Build source reader.
	switch src.pathType {
	case PathLocal:
		job.Src = NewLocalReader(src.localPath, src.localInfo.Size())
	case PathProton:
		fh, err := dc.OpenFile(ctx, src.link)
		if err != nil {
			return nil, fmt.Errorf("cp: %s: %w", src.raw, err)
		}
		store := driveClient.NewBlockStore(dc.Session, nil)
		job.Src = driveClient.NewProtonReader(fh.LinkID, fh.Blocks, fh.SessionKey, fh.FileSize, nil, store)
	}

	// Build destination writer. Pre-create local files so workers can
	// write blocks at arbitrary offsets into an existing file.
	switch dst.pathType {
	case PathLocal:
		f, err := os.Create(dst.localPath)
		if err != nil {
			return nil, fmt.Errorf("cp: %s: %w", dst.localPath, err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("cp: %s: %w", dst.localPath, err)
		}
		job.Dst = NewLocalWriter(dst.localPath)
	case PathProton:
		name := filepath.Base(dst.raw)
		if src.pathType == PathLocal {
			name = filepath.Base(src.localPath)
		}
		fh, err := dc.CreateFile(ctx, dst.share, dst.link, name)
		if err != nil {
			return nil, fmt.Errorf("cp: %s: %w", dst.raw, err)
		}
		store := driveClient.NewBlockStore(dc.Session, nil)
		job.Dst = driveClient.NewProtonWriter(fh, store, dc.Session)
	}

	return &job, nil
}
