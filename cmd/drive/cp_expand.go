package driveCmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
	driveClient "github.com/major0/proton-cli/api/drive/client"
)

// ensureDestDir creates a destination subdirectory at relPath under dstBase.
func ensureDestDir(ctx context.Context, dc *driveClient.Client, dstBase *resolvedEndpoint, relPath string) {
	switch dstBase.pathType {
	case PathLocal:
		dstDir := filepath.Join(dstBase.localPath, relPath)
		if err := os.MkdirAll(dstDir, 0700); err != nil {
			fmt.Fprintf(os.Stderr, "cp: mkdir %s: %v\n", dstDir, err)
		}
	case PathProton:
		if _, err := dc.MkDirAll(ctx, dstBase.share, dstBase.link, relPath); err != nil {
			fmt.Fprintf(os.Stderr, "cp: mkdir %s: %v\n", relPath, err)
		}
	}
}

// makeFileDst constructs a resolved destination endpoint for a file at
// relPath under dstBase.
func makeFileDst(dstBase *resolvedEndpoint, relPath string) *resolvedEndpoint {
	switch dstBase.pathType {
	case PathLocal:
		return &resolvedEndpoint{
			pathType:  PathLocal,
			raw:       filepath.Join(dstBase.localPath, relPath),
			localPath: filepath.Join(dstBase.localPath, relPath),
		}
	case PathProton:
		return &resolvedEndpoint{
			pathType: PathProton,
			raw:      relPath,
			link:     dstBase.link,
			share:    dstBase.share,
		}
	}
	return nil
}

// expandRecursive walks a source directory and returns CopyJobs for all
// files. Destination subdirectories are created as encountered (breadth-
// first for Proton sources, natural walk order for local). Directories
// never become CopyJobs — only files with block data do.
func expandRecursive(ctx context.Context, dc *driveClient.Client, src, dstBase *resolvedEndpoint, opts cpOptions) ([]CopyJob, []preserveEntry, error) {
	switch src.pathType {
	case PathLocal:
		return expandLocalRecursive(ctx, dc, src, dstBase, opts)
	case PathProton:
		return expandProtonRecursive(ctx, dc, src, dstBase, opts)
	}
	return nil, nil, nil
}

// expandLocalRecursive walks a local source directory tree.
func expandLocalRecursive(ctx context.Context, dc *driveClient.Client, src, dstBase *resolvedEndpoint, opts cpOptions) ([]CopyJob, []preserveEntry, error) {
	var jobs []CopyJob
	var preserves []preserveEntry
	srcRoot := src.localPath

	// Create the top-level dest directory.
	switch dstBase.pathType {
	case PathLocal:
		if err := os.MkdirAll(dstBase.localPath, 0700); err != nil {
			return nil, nil, fmt.Errorf("cp: mkdir %s: %w", dstBase.localPath, err)
		}
	case PathProton:
		if _, err := dc.MkDirAll(ctx, dstBase.share, dstBase.link, filepath.Base(dstBase.raw)); err != nil {
			return nil, nil, fmt.Errorf("cp: mkdir %s: %w", dstBase.raw, err)
		}
	}

	err := filepath.WalkDir(srcRoot, func(path string, d os.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", path, walkErr)
			return nil // continue on error
		}

		// Compute relative path from source root.
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", path, err)
			return nil
		}
		if rel == "." {
			return nil // skip the root itself
		}

		// Symlink handling.
		if d.Type()&os.ModeSymlink != 0 {
			if !opts.dereference {
				fmt.Fprintf(os.Stderr, "cp: %s: skipping symbolic link\n", path)
				return nil
			}
			// -L: follow the symlink — re-stat to get the target info.
			// WalkDir won't descend into symlinked dirs, so we only
			// handle symlinked files here.
		}

		if d.IsDir() {
			ensureDestDir(ctx, dc, dstBase, rel)
			return nil
		}

		// Regular file — build a CopyJob.
		// Use os.Stat (not d.Info) when following symlinks so we get
		// the target's size, not the symlink's path length.
		var info os.FileInfo
		if d.Type()&os.ModeSymlink != 0 {
			info, err = os.Stat(path)
		} else {
			info, err = d.Info()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", path, err)
			return nil
		}

		fileSrc := &resolvedEndpoint{
			pathType:  PathLocal,
			raw:       path,
			localPath: path,
			localInfo: info,
		}

		fileDst := makeFileDst(dstBase, rel)

		if err := handleConflict(ctx, dc, fileDst, opts); err != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", path, err)
			return nil
		}

		job, err := buildCopyJob(ctx, dc, fileSrc, fileDst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", path, err)
			return nil
		}
		jobs = append(jobs, *job)

		// Collect preservation metadata for local→local.
		if fileDst.pathType == PathLocal {
			preserves = append(preserves, preserveEntry{
				dstPath: fileDst.localPath,
				mode:    info.Mode().Perm(),
				mtime:   info.ModTime(),
			})
		}

		return nil
	})

	if err != nil {
		return jobs, preserves, err
	}
	return jobs, preserves, nil
}

// expandProtonRecursive walks a Proton source directory tree using
// breadth-first TreeWalk.
func expandProtonRecursive(ctx context.Context, dc *driveClient.Client, src, dstBase *resolvedEndpoint, opts cpOptions) ([]CopyJob, []preserveEntry, error) {
	var jobs []CopyJob

	results := make(chan driveClient.WalkEntry, 64)
	var walkErr error
	go func() {
		defer close(results)
		walkErr = dc.TreeWalk(ctx, src.link, "", drive.BreadthFirst, results)
	}()

	for entry := range results {
		if ctx.Err() != nil {
			return jobs, nil, ctx.Err()
		}

		// Skip the root itself.
		if entry.Depth == 0 {
			continue
		}

		if entry.Link.Type() == proton.LinkTypeFolder {
			ensureDestDir(ctx, dc, dstBase, entry.Path)
			continue
		}

		// Regular file — build a CopyJob.
		fileSrc := &resolvedEndpoint{
			pathType: PathProton,
			raw:      entry.Path,
			link:     entry.Link,
			share:    src.share,
		}

		fileDst := makeFileDst(dstBase, entry.Path)

		if err := handleConflict(ctx, dc, fileDst, opts); err != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", entry.Path, err)
			continue
		}

		job, err := buildCopyJob(ctx, dc, fileSrc, fileDst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cp: %s: %v\n", entry.Path, err)
			continue
		}
		jobs = append(jobs, *job)
	}

	if walkErr != nil {
		return jobs, nil, walkErr
	}
	return jobs, nil, nil
}
