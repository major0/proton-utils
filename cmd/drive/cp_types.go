package driveCmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api/drive"
)

// cpOptions holds the resolved copy options, constructed once in runCp
// from cpFlags. All sub-functions receive this struct instead of reading
// the package-level cpFlags global directly.
type cpOptions struct {
	recursive   bool
	dereference bool
	removeDest  bool
	force       bool
	backup      bool
	preserve    string
	verbose     bool
	progress    bool
}

// PathType distinguishes local filesystem paths from Proton Drive paths.
type PathType int

const (
	// PathLocal is a local filesystem path.
	PathLocal PathType = iota
	// PathProton is a Proton Drive path (proton:// URI).
	PathProton
)

// pathArg is a parsed command argument with its classified type.
type pathArg struct {
	raw      string
	pathType PathType
}

// classifyPath returns PathProton if arg starts with "proton://", PathLocal otherwise.
func classifyPath(arg string) PathType {
	if strings.HasPrefix(arg, "proton://") {
		return PathProton
	}
	return PathLocal
}

// resolvedEndpoint holds the result of resolving a source or destination path.
// Exactly one variant is populated based on pathType.
type resolvedEndpoint struct {
	pathType PathType
	raw      string // original argument string

	// Local path resolution (pathType == PathLocal)
	localPath string      // cleaned absolute path
	localInfo os.FileInfo // from os.Stat

	// Proton path resolution (pathType == PathProton)
	link  *drive.Link
	share *drive.Share
}

// isDir returns true if the resolved endpoint is a directory.
func (r *resolvedEndpoint) isDir() bool {
	if r.pathType == PathLocal {
		return r.localInfo != nil && r.localInfo.IsDir()
	}
	return r.link != nil && r.link.Type() == proton.LinkTypeFolder
}

// basename returns the name of the resolved endpoint.
func (r *resolvedEndpoint) basename() string {
	if r.pathType == PathLocal {
		return filepath.Base(r.localPath)
	}
	if r.link != nil {
		name, err := r.link.Name()
		if err != nil {
			return filepath.Base(r.raw)
		}
		return name
	}
	return filepath.Base(r.raw)
}
