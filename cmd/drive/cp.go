package driveCmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ProtonMail/go-proton-api"
	api "github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
	apiPool "github.com/major0/proton-cli/api/pool"
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
	targetDir   string // -t, --target-directory
	removeDest  bool   // --remove-destination (trash Proton / remove local before copy)
	force       bool   // -f, --force (overwrite destination)
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
	f.StringVarP(&cpFlags.targetDir, "target-directory", "t", "", "Copy all sources into this directory")
	cli.BoolFlag(f, &cpFlags.removeDest, "remove-destination", false, "Trash/remove destination before copy (disables versioning)")
	cli.BoolFlagP(f, &cpFlags.force, "force", "f", false, "Overwrite existing destination files")
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
		force:       cpFlags.force,
		backup:      cpFlags.backup,
		preserve:    cpFlags.preserve,
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
			// For local destinations, stat the child to detect conflicts.
			if fileDst.pathType == PathLocal {
				info, err := os.Stat(fileDst.localPath)
				if err == nil {
					fileDst.localInfo = info
				}
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

		// Same-file check before conflict handling — "source and
		// destination are the same" takes priority over "file exists".
		if srcEp.pathType == PathLocal && fileDst.pathType == PathLocal &&
			srcEp.localPath == fileDst.localPath {
			return fmt.Errorf("cp: %s: source and destination are the same", srcEp.raw)
		}
		if srcEp.pathType == PathProton && fileDst.pathType == PathProton &&
			srcEp.link != nil && fileDst.link != nil &&
			srcEp.link.LinkID() == fileDst.link.LinkID() {
			return fmt.Errorf("cp: %s: source and destination are the same", srcEp.raw)
		}

		if err := handleConflict(ctx, dc, fileDst, opts); err != nil {
			return err
		}

		job, err := buildCopyJob(ctx, dc, srcEp, fileDst, opts)
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

	// Use the session pool when available (Proton paths), otherwise
	// create a local pool for local-only copies.
	var wp *apiPool.Pool
	if dc != nil && dc.Session.Pool != nil {
		wp = dc.Session.Pool
	} else {
		wp = apiPool.New(ctx, api.DefaultMaxWorkers())
	}

	if err := RunPipeline(ctx, wp, jobs, transferOpts(opts)); err != nil {
		return err
	}

	// Apply preserved attributes after all blocks are written.
	applyPreserve(preserves, opts)
	return nil
}

// buildCopyJob constructs a CopyJob from resolved source and destination
// endpoints. For Proton endpoints, uses CreateFile/OpenFile to get the
// FileHandle with revision, session key, and block info.
func buildCopyJob(ctx context.Context, dc *driveClient.Client, src, dst *resolvedEndpoint, opts cpOptions) (*CopyJob, error) {
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
			switch {
			case errors.Is(err, proton.ErrFileNameExist):
				if !opts.force && !opts.removeDest {
					return nil, fmt.Errorf("cp: %s: file exists (use -f to overwrite)", name)
				}
				// Lookup the blocker to determine type and get the Link.
				blocker, lookupErr := dst.link.Lookup(ctx, name)
				if lookupErr != nil {
					return nil, fmt.Errorf("cp: %s: lookup: %w", name, lookupErr)
				}
				switch {
				case blocker == nil:
					// Race: blocker removed between CreateFile and Lookup — retry.
					fh, err = dc.CreateFile(ctx, dst.share, dst.link, name)
					if err != nil {
						return nil, fmt.Errorf("cp: %s: %w", name, err)
					}
				case blocker.Type() == proton.LinkTypeFolder:
					return nil, fmt.Errorf("cp: %s: cannot overwrite directory with non-directory", name)
				case blocker.State() == proton.LinkStateTrashed:
					// Trashed link still occupies the name hash — permanently delete, then create new.
					if delErr := dc.Remove(ctx, dst.share, blocker, drive.RemoveOpts{Permanent: true}); delErr != nil {
						return nil, fmt.Errorf("cp: %s: remove trashed: %w", name, delErr)
					}
					fh, err = dc.CreateFile(ctx, dst.share, dst.link, name)
					if err != nil {
						return nil, fmt.Errorf("cp: %s: %w", name, err)
					}
				default:
					// Active file or draft — check if it has a committed revision.
					pLink := blocker.ProtonLink()
					hasActiveRevision := pLink.FileProperties != nil &&
						pLink.FileProperties.ActiveRevision.ID != "" &&
						pLink.FileProperties.ActiveRevision.State == proton.RevisionStateActive
					if !hasActiveRevision {
						// No committed revision — delete the link and create fresh.
						if delErr := dc.Remove(ctx, dst.share, blocker, drive.RemoveOpts{Permanent: true}); delErr != nil {
							return nil, fmt.Errorf("cp: %s: remove draft link: %w", name, delErr)
						}
						fh, err = dc.CreateFile(ctx, dst.share, dst.link, name)
						if err != nil {
							return nil, fmt.Errorf("cp: %s: %w", name, err)
						}
					} else {
						// Has active revision — overwrite via CreateRevision.
						fh, err = dc.OverwriteFile(ctx, dst.share, blocker)
						if err != nil {
							return nil, fmt.Errorf("cp: %s: %w", name, err)
						}
					}
				}

			case errors.Is(err, proton.ErrADraftExist):
				if !opts.force && !opts.removeDest {
					return nil, fmt.Errorf("cp: %s: file exists (use -f to overwrite)", name)
				}
				// Draft-only link or stale draft — lookup and handle.
				blocker, lookupErr := dst.link.Lookup(ctx, name)
				if lookupErr != nil {
					return nil, fmt.Errorf("cp: %s: lookup draft: %w", name, lookupErr)
				}
				switch {
				case blocker != nil && blocker.State() == proton.LinkStateDraft:
					// Draft-only link: delete and recreate.
					if delErr := dc.Remove(ctx, dst.share, blocker, drive.RemoveOpts{Permanent: true}); delErr != nil {
						return nil, fmt.Errorf("cp: %s: remove draft: %w", name, delErr)
					}
					fh, err = dc.CreateFile(ctx, dst.share, dst.link, name)
					if err != nil {
						return nil, fmt.Errorf("cp: %s: %w", name, err)
					}
				case blocker != nil:
					// Active file with stale draft — overwrite.
					fh, err = dc.OverwriteFile(ctx, dst.share, blocker)
					if err != nil {
						return nil, fmt.Errorf("cp: %s: %w", name, err)
					}
				default:
					return nil, fmt.Errorf("cp: %s: %w", name, err)
				}

			default:
				return nil, fmt.Errorf("cp: %s: %w", dst.raw, err)
			}
		}
		store := driveClient.NewBlockStore(dc.Session, nil)
		job.Dst = driveClient.NewProtonWriter(fh, store, dc.Session)
	}

	return &job, nil
}
