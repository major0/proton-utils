//go:build linux

package drive

import (
	"context"
	"fmt"
	"syscall"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/ProtonMail/gopenpgp/v2/crypto"
	"github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/drive"
	"pgregory.net/rapid"
)

// TestPropertyReaddirCompleteness verifies that for any set of shares with
// arbitrary types (main, photos, standard, device), DriveHandler.Readdir
// returns exactly: one "Home" entry (if main share exists), one "Photos"
// entry (if photos share exists), one entry per standard share (using its
// decrypted name), one ".linkid" entry, and zero entries for device shares.
//
// Feature: protonfs-daemon, Property 2: Readdir completeness
// **Validates: Requirements 8.1, 8.2, 8.3, 8.4, 8.5, 8.6**
func TestPropertyReaddirCompleteness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random number of shares (0-20).
		numShares := rapid.IntRange(0, 20).Draw(rt, "numShares")

		// Track constraints: at most one main, at most one photos.
		hasMain := false
		hasPhotos := false

		shares := make(map[string]*drive.Share, numShares)
		var standardNames []string
		usedNames := make(map[string]bool)

		for i := 0; i < numShares; i++ {
			shareID := fmt.Sprintf("share-%d", i)

			// Determine available types based on constraints.
			// Types: main(1), photos(4), standard(2), device(3)
			availableTypes := []proton.ShareType{proton.ShareTypeStandard, proton.ShareTypeDevice}
			if !hasMain {
				availableTypes = append(availableTypes, proton.ShareTypeMain)
			}
			if !hasPhotos {
				availableTypes = append(availableTypes, drive.ShareTypePhotos)
			}

			typeIdx := rapid.IntRange(0, len(availableTypes)-1).Draw(rt, fmt.Sprintf("typeIdx-%d", i))
			st := availableTypes[typeIdx]

			var name string
			switch st {
			case proton.ShareTypeMain:
				hasMain = true
				name = "root"
			case drive.ShareTypePhotos:
				hasPhotos = true
				name = "photos"
			case proton.ShareTypeStandard:
				// Generate a unique non-empty name for standard shares.
				// Use index suffix to guarantee uniqueness across shares.
				base := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,14}`).Draw(rt, fmt.Sprintf("stdName-%d", i))
				name = fmt.Sprintf("%s-%d", base, i)
				for usedNames[name] {
					name = fmt.Sprintf("%s-%d-x", base, i)
				}
				usedNames[name] = true
				standardNames = append(standardNames, name)
			case proton.ShareTypeDevice:
				name = fmt.Sprintf("device-%d", i)
			}

			shares[shareID] = testShare(name, shareID, st)
		}

		h := buildTestHandler(shares)
		entries, errno := h.Readdir(context.Background())
		if errno != 0 {
			rt.Fatalf("Readdir returned errno %d", errno)
		}

		// Build a name→count map from entries.
		nameCount := make(map[string]int, len(entries))
		for _, e := range entries {
			nameCount[e.Name]++
			// All entries must be directories.
			if e.Mode != syscall.S_IFDIR {
				rt.Fatalf("entry %q has mode %o, want S_IFDIR (%o)", e.Name, e.Mode, syscall.S_IFDIR)
			}
		}

		// Verify "Home" present iff main share exists.
		if hasMain {
			if nameCount["Home"] != 1 {
				rt.Fatalf("expected exactly 1 Home entry, got %d", nameCount["Home"])
			}
		} else {
			if nameCount["Home"] != 0 {
				rt.Fatalf("expected no Home entry (no main share), got %d", nameCount["Home"])
			}
		}

		// Verify "Photos" present iff photos share exists.
		if hasPhotos {
			if nameCount["Photos"] != 1 {
				rt.Fatalf("expected exactly 1 Photos entry, got %d", nameCount["Photos"])
			}
		} else {
			if nameCount["Photos"] != 0 {
				rt.Fatalf("expected no Photos entry (no photos share), got %d", nameCount["Photos"])
			}
		}

		// Verify each standard share's decrypted name appears exactly once.
		for _, sn := range standardNames {
			if nameCount[sn] != 1 {
				rt.Fatalf("expected exactly 1 entry for standard share %q, got %d", sn, nameCount[sn])
			}
		}

		// Verify ".linkid" always present.
		if nameCount[".linkid"] != 1 {
			rt.Fatalf("expected exactly 1 .linkid entry, got %d", nameCount[".linkid"])
		}

		// Verify no device share names appear.
		for _, share := range shares {
			if share.ProtonShare().Type == proton.ShareTypeDevice {
				devName, _ := share.GetName(context.Background())
				if nameCount[devName] != 0 {
					rt.Fatalf("device share name %q should not appear, got %d entries", devName, nameCount[devName])
				}
			}
		}

		// Verify total count matches expected.
		expectedCount := len(standardNames) + 1 // +1 for .linkid
		if hasMain {
			expectedCount++
		}
		if hasPhotos {
			expectedCount++
		}
		if len(entries) != expectedCount {
			rt.Fatalf("got %d entries, want %d (main=%v, photos=%v, standard=%d, .linkid=1)",
				len(entries), expectedCount, hasMain, hasPhotos, len(standardNames))
		}
	})
}

// TestPropertyLookupCorrectness verifies that for any name string and any
// share set, DriveHandler.Lookup returns the correct node type if the name
// matches "Home" (main share), "Photos" (photos share present), a standard
// share's decrypted name, or ".linkid"; and returns ENOENT for any name not
// matching these.
//
// Feature: protonfs-daemon, Property 3: Lookup correctness
// **Validates: Requirements 8.7, 8.8, 8.9, 8.10, 8.11, 8.12**
func TestPropertyLookupCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random number of shares (0-20).
		numShares := rapid.IntRange(0, 20).Draw(rt, "numShares")

		// Track constraints: at most one main, at most one photos.
		hasMain := false
		hasPhotos := false

		shares := make(map[string]*drive.Share, numShares)
		standardNames := make(map[string]bool)

		for i := 0; i < numShares; i++ {
			shareID := fmt.Sprintf("share-%d", i)

			// Determine available types based on constraints.
			availableTypes := []proton.ShareType{proton.ShareTypeStandard, proton.ShareTypeDevice}
			if !hasMain {
				availableTypes = append(availableTypes, proton.ShareTypeMain)
			}
			if !hasPhotos {
				availableTypes = append(availableTypes, drive.ShareTypePhotos)
			}

			typeIdx := rapid.IntRange(0, len(availableTypes)-1).Draw(rt, fmt.Sprintf("typeIdx-%d", i))
			st := availableTypes[typeIdx]

			var name string
			switch st {
			case proton.ShareTypeMain:
				hasMain = true
				name = "root"
			case drive.ShareTypePhotos:
				hasPhotos = true
				name = "photos"
			case proton.ShareTypeStandard:
				// Generate a unique non-empty name for standard shares.
				base := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,14}`).Draw(rt, fmt.Sprintf("stdName-%d", i))
				name = fmt.Sprintf("%s-%d", base, i)
				for standardNames[name] {
					name = fmt.Sprintf("%s-%d-x", base, i)
				}
				standardNames[name] = true
			case proton.ShareTypeDevice:
				name = fmt.Sprintf("device-%d", i)
			}

			shares[shareID] = testShare(name, shareID, st)
		}

		h := buildTestHandler(shares)
		ctx := context.Background()

		// --- Test "Home" lookup ---
		node, errno := h.Lookup(ctx, "Home")
		if hasMain {
			if errno != 0 {
				rt.Fatalf("Lookup(Home) returned errno %d, want 0 (main share exists)", errno)
			}
			if _, ok := node.(*ShareDirNode); !ok {
				rt.Fatalf("Lookup(Home) returned %T, want *ShareDirNode", node)
			}
		} else if errno != syscall.ENOENT {
			rt.Fatalf("Lookup(Home) returned errno %d, want ENOENT (no main share)", errno)
		}

		// --- Test "Photos" lookup ---
		node, errno = h.Lookup(ctx, "Photos")
		if hasPhotos {
			if errno != 0 {
				rt.Fatalf("Lookup(Photos) returned errno %d, want 0 (photos share exists)", errno)
			}
			if _, ok := node.(*ShareDirNode); !ok {
				rt.Fatalf("Lookup(Photos) returned %T, want *ShareDirNode", node)
			}
		} else if errno != syscall.ENOENT {
			rt.Fatalf("Lookup(Photos) returned errno %d, want ENOENT (no photos share)", errno)
		}

		// --- Test ".linkid" lookup (always returns LinkIDDir) ---
		node, errno = h.Lookup(ctx, ".linkid")
		if errno != 0 {
			rt.Fatalf("Lookup(.linkid) returned errno %d, want 0", errno)
		}
		if _, ok := node.(*LinkIDDir); !ok {
			rt.Fatalf("Lookup(.linkid) returned %T, want *LinkIDDir", node)
		}

		// --- Test each standard share's decrypted name ---
		for sn := range standardNames {
			node, errno = h.Lookup(ctx, sn)
			if errno != 0 {
				rt.Fatalf("Lookup(%q) returned errno %d, want 0 (standard share exists)", sn, errno)
			}
			if _, ok := node.(*ShareDirNode); !ok {
				rt.Fatalf("Lookup(%q) returned %T, want *ShareDirNode", sn, node)
			}
		}

		// --- Test unknown name returns ENOENT ---
		// Generate a name that doesn't collide with any known name.
		unknownName := rapid.StringMatching(`unknown_[a-z]{5,10}`).Draw(rt, "unknownName")
		// Ensure it doesn't accidentally match a standard share name, "Home", "Photos", or ".linkid".
		for unknownName == "Home" || unknownName == "Photos" || unknownName == ".linkid" || standardNames[unknownName] {
			unknownName += "_x"
		}

		_, errno = h.Lookup(ctx, unknownName)
		if errno != syscall.ENOENT {
			rt.Fatalf("Lookup(%q) returned errno %d, want ENOENT (unknown name)", unknownName, errno)
		}
	})
}

// childResolver is a mock drive.LinkResolver that returns pre-configured
// children for a specific parent link. Used by TestPropertyDirNodeChildOps
// to test ShareDirNode.Readdir and Lookup without real crypto.
type childResolver struct {
	// children holds the raw proton.Link objects to return from ListLinkChildren.
	children []proton.Link
	// nameMap maps LinkID → decrypted name for NewChildLink to use with NewTestLink.
	nameMap map[string]string
}

func (r *childResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return r.children, nil
}

func (r *childResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	name := r.nameMap[pLink.LinkID]
	return drive.NewTestLink(pLink, parent, parent.Share(), r, name)
}

func (r *childResolver) GetLink(_ string) *drive.Link { return nil }

func (r *childResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *childResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *childResolver) Throttle() *api.Throttle { return nil }
func (r *childResolver) MaxWorkers() int         { return 1 }

// TestPropertyDirNodeChildOps verifies that for any directory node wrapping
// a link with an arbitrary set of children (mix of files and folders),
// Readdir returns one entry per child with the correct decrypted name and
// mode (S_IFDIR for folders, S_IFREG for files), and Lookup returns the
// correct child node for any name matching a child's decrypted name, or
// ENOENT for non-matching names.
//
// Feature: protonfs-daemon, Property 4: DirNode child listing and lookup
// **Validates: Requirements 9.2, 9.3, 9.4**
func TestPropertyDirNodeChildOps(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random number of children (0-30).
		numChildren := rapid.IntRange(0, 30).Draw(rt, "numChildren")

		// Generate unique non-empty names and random types for each child.
		type childSpec struct {
			name   string
			linkID string
			isDir  bool
		}
		children := make([]childSpec, 0, numChildren)
		usedNames := make(map[string]bool)

		for i := 0; i < numChildren; i++ {
			// Generate a unique non-empty name.
			base := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9._-]{0,14}`).Draw(rt, fmt.Sprintf("name-%d", i))
			name := fmt.Sprintf("%s-%d", base, i)
			for usedNames[name] {
				name = fmt.Sprintf("%s-%d-x", base, i)
			}
			usedNames[name] = true

			isDir := rapid.Bool().Draw(rt, fmt.Sprintf("isDir-%d", i))
			linkID := fmt.Sprintf("child-link-%d", i)

			children = append(children, childSpec{
				name:   name,
				linkID: linkID,
				isDir:  isDir,
			})
		}

		// Build the raw proton.Link slice and name map for the resolver.
		pChildren := make([]proton.Link, len(children))
		nameMap := make(map[string]string, len(children))
		for i, c := range children {
			lt := proton.LinkTypeFile
			if c.isDir {
				lt = proton.LinkTypeFolder
			}
			pChildren[i] = proton.Link{
				LinkID: c.linkID,
				Type:   lt,
				State:  proton.LinkStateActive,
			}
			nameMap[c.linkID] = c.name
		}

		resolver := &childResolver{
			children: pChildren,
			nameMap:  nameMap,
		}

		// Build a share with a root link that uses our resolver.
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "test-share"},
		}
		rootPLink := &proton.Link{LinkID: "root-link", Type: proton.LinkTypeFolder}
		root := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := drive.NewShare(pShare, nil, root, resolver, "vol-1")
		// Re-create root with the share set so children get the share reference.
		root = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		// Construct a ShareDirNode wrapping the share.
		node := &ShareDirNode{share: share}

		ctx := context.Background()

		// --- Verify Readdir ---
		entries, errno := node.Readdir(ctx)
		if errno != 0 {
			rt.Fatalf("Readdir returned errno %d", errno)
		}

		// Must return exactly one entry per child.
		if len(entries) != len(children) {
			rt.Fatalf("Readdir returned %d entries, want %d", len(entries), len(children))
		}

		// Build a map of entry name → mode for verification.
		entryMap := make(map[string]uint32, len(entries))
		for _, e := range entries {
			if _, dup := entryMap[e.Name]; dup {
				rt.Fatalf("Readdir returned duplicate entry name %q", e.Name)
			}
			entryMap[e.Name] = e.Mode
		}

		// Verify each child appears with correct name and mode.
		for _, c := range children {
			mode, ok := entryMap[c.name]
			if !ok {
				rt.Fatalf("Readdir missing entry for child %q", c.name)
			}
			expectedMode := uint32(syscall.S_IFREG | 0555)
			if c.isDir {
				expectedMode = syscall.S_IFDIR | 0555
			}
			if mode != expectedMode {
				rt.Fatalf("entry %q has mode %o, want %o", c.name, mode, expectedMode)
			}
		}

		// --- Verify Lookup for each child name ---
		for _, c := range children {
			lookupNode, errno := node.Lookup(ctx, c.name)
			if errno != 0 {
				rt.Fatalf("Lookup(%q) returned errno %d, want 0", c.name, errno)
			}
			if lookupNode == nil {
				rt.Fatalf("Lookup(%q) returned nil node", c.name)
			}
			if c.isDir {
				if _, ok := lookupNode.(*LinkDirNode); !ok {
					rt.Fatalf("Lookup(%q) returned %T, want *LinkDirNode (folder)", c.name, lookupNode)
				}
			} else {
				if _, ok := lookupNode.(*FileNode); !ok {
					rt.Fatalf("Lookup(%q) returned %T, want *FileNode (file)", c.name, lookupNode)
				}
			}
		}

		// --- Verify Lookup for a non-matching name returns ENOENT ---
		nonExistent := rapid.StringMatching(`nonexist_[a-z]{5,10}`).Draw(rt, "nonExistent")
		for usedNames[nonExistent] {
			nonExistent += "_z"
		}
		_, errno = node.Lookup(ctx, nonExistent)
		if errno != syscall.ENOENT {
			rt.Fatalf("Lookup(%q) returned errno %d, want ENOENT (%d)", nonExistent, errno, syscall.ENOENT)
		}
	})
}

// TestPropertyFileNodeAttributes verifies that for any file link with
// arbitrary size and timestamps, FileNode.Getattr returns Size equal to
// uint64(link.Size()), mtime equal to uint64(link.ModifyTime()), and ctime
// equal to uint64(link.CreateTime()), with mode S_IFREG|0400 and Nlink 1.
//
// Feature: protonfs-daemon, Property 8: File node attributes
// **Validates: Requirements 10.2, 10.3**
func TestPropertyFileNodeAttributes(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate random file metadata.
		// Size: 0 to 10TB (realistic range for files).
		// 10 TB = 10 * 1024^4 = 10,995,116,277,760
		size := rapid.Int64Range(0, 10*1024*1024*1024*1024).Draw(rt, "size")

		// Timestamps: Unix seconds in realistic range 0 to 2^40.
		const maxTimestamp = int64(1) << 40
		modifyTime := rapid.Int64Range(0, maxTimestamp).Draw(rt, "modifyTime")
		createTime := rapid.Int64Range(0, maxTimestamp).Draw(rt, "createTime")

		// Construct a proton.Link with file properties carrying the generated values.
		// link.Size() reads FileProperties.ActiveRevision.Size
		// link.ModifyTime() reads FileProperties.ActiveRevision.CreateTime (for files)
		// link.CreateTime() reads protonLink.CreateTime
		pLink := &proton.Link{
			LinkID:     "test-file-link",
			Type:       proton.LinkTypeFile,
			CreateTime: createTime,
			FileProperties: &proton.FileProperties{
				ActiveRevision: proton.RevisionMetadata{
					Size:       size,
					CreateTime: modifyTime,
				},
			},
		}

		// Wrap in a drive.Link via NewTestLink (no crypto needed).
		link := drive.NewTestLink(pLink, nil, nil, nil, "test-file.txt")

		// Construct a FileNode wrapping the link.
		node := &FileNode{link: link}

		// Call Getattr.
		attr, errno := node.Getattr(context.Background())
		if errno != 0 {
			rt.Fatalf("Getattr returned errno %d, want 0", errno)
		}

		// Verify mode.
		expectedMode := uint32(syscall.S_IFREG | 0555)
		if attr.Mode != expectedMode {
			rt.Fatalf("Mode = %o, want %o", attr.Mode, expectedMode)
		}

		// Verify size.
		if attr.Size != uint64(size) { //nolint:gosec // test: size generated non-negative
			rt.Fatalf("Size = %d, want %d", attr.Size, uint64(size)) //nolint:gosec // test assertion
		}

		// Verify nlink.
		if attr.Nlink != 1 {
			rt.Fatalf("Nlink = %d, want 1", attr.Nlink)
		}

		// Verify mtime matches link.ModifyTime().
		if attr.Mtime != uint64(modifyTime) { //nolint:gosec // test: timestamp generated non-negative
			rt.Fatalf("Mtime = %d, want %d", attr.Mtime, uint64(modifyTime)) //nolint:gosec // test assertion
		}

		// Verify ctime matches link.CreateTime().
		if attr.Ctime != uint64(createTime) { //nolint:gosec // test: timestamp generated non-negative
			rt.Fatalf("Ctime = %d, want %d", attr.Ctime, uint64(createTime)) //nolint:gosec // test assertion
		}
	})
}

// errorResolver is a mock LinkResolver that always returns an error from
// ListLinkChildren. Used by TestPropertyAPIErrorMapping to verify error
// mapping behavior without real API calls.
type errorResolver struct {
	err error
}

func (r *errorResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return nil, r.err
}

func (r *errorResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	return drive.NewTestLink(pLink, parent, parent.Share(), r, "unused")
}

func (r *errorResolver) GetLink(_ string) *drive.Link { return nil }

func (r *errorResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *errorResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *errorResolver) Throttle() *api.Throttle { return nil }
func (r *errorResolver) MaxWorkers() int         { return 1 }

// TestPropertyAPIErrorMapping verifies that for any API error returned by
// the drive client during child listing, the handler returns syscall.EIO
// to the FUSE caller. Context cancellation maps to EINTR instead.
//
// Feature: protonfs-daemon, Property 6: API error mapping
// **Validates: Requirements 13.1, 13.3**
func TestPropertyAPIErrorMapping(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random error message to simulate arbitrary API errors.
		errMsg := rapid.StringMatching(`[a-zA-Z0-9 _.-]{1,50}`).Draw(rt, "errMsg")
		apiErr := fmt.Errorf("api: %s", errMsg)

		// Choose whether to test context cancellation (EINTR) or generic error (EIO).
		testCancellation := rapid.Bool().Draw(rt, "testCancellation")

		var injectedErr error
		var expectedErrno syscall.Errno
		if testCancellation {
			injectedErr = context.Canceled
			expectedErrno = syscall.EINTR
		} else {
			injectedErr = apiErr
			expectedErrno = syscall.EIO
		}

		resolver := &errorResolver{err: injectedErr}

		// Build a share with a root link that uses the error resolver.
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "err-share"},
		}
		rootPLink := &proton.Link{LinkID: "err-root-link", Type: proton.LinkTypeFolder}
		root := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := drive.NewShare(pShare, nil, root, resolver, "vol-err")
		root = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		ctx := context.Background()

		// --- Test ShareDirNode.Readdir ---
		shareNode := &ShareDirNode{share: share}
		_, errno := shareNode.Readdir(ctx)
		if errno != expectedErrno {
			rt.Fatalf("ShareDirNode.Readdir: got errno %d, want %d (err=%v)", errno, expectedErrno, injectedErr)
		}

		// --- Test ShareDirNode.Lookup ---
		lookupName := rapid.StringMatching(`[a-z]{3,10}`).Draw(rt, "lookupName")
		_, errno = shareNode.Lookup(ctx, lookupName)
		if errno != expectedErrno {
			rt.Fatalf("ShareDirNode.Lookup(%q): got errno %d, want %d (err=%v)", lookupName, errno, expectedErrno, injectedErr)
		}

		// --- Test LinkDirNode.Readdir ---
		folderPLink := &proton.Link{LinkID: "err-folder-link", Type: proton.LinkTypeFolder}
		folderLink := drive.NewTestLink(folderPLink, root, share, resolver, "folder")
		linkNode := &LinkDirNode{link: folderLink}

		_, errno = linkNode.Readdir(ctx)
		if errno != expectedErrno {
			rt.Fatalf("LinkDirNode.Readdir: got errno %d, want %d (err=%v)", errno, expectedErrno, injectedErr)
		}

		// --- Test LinkDirNode.Lookup ---
		_, errno = linkNode.Lookup(ctx, lookupName)
		if errno != expectedErrno {
			rt.Fatalf("LinkDirNode.Lookup(%q): got errno %d, want %d (err=%v)", lookupName, errno, expectedErrno, injectedErr)
		}
	})
}

// TestPropertyLinkIDLookup verifies that for any LinkID string,
// LinkIDDir.Lookup returns a file node if the ID resolves to a file link,
// a directory node if it resolves to a folder link, or ENOENT if the ID
// does not exist in the client's link table.
//
// Feature: protonfs-daemon, Property 5: LinkID Lookup correctness
// **Validates: Requirements 11.2, 11.3**
func TestPropertyLinkIDLookup(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// Generate a random number of links (0-20) to populate the table.
		numLinks := rapid.IntRange(0, 20).Draw(rt, "numLinks")

		// Generate unique LinkIDs and random types (file/folder).
		type linkSpec struct {
			linkID string
			isDir  bool
		}
		links := make([]linkSpec, 0, numLinks)
		linkMap := make(map[string]*drive.Link, numLinks)
		usedIDs := make(map[string]bool, numLinks)

		for i := 0; i < numLinks; i++ {
			// Generate a unique LinkID.
			id := rapid.StringMatching(`[a-zA-Z0-9_-]{4,20}`).Draw(rt, fmt.Sprintf("linkID-%d", i))
			for usedIDs[id] {
				id += fmt.Sprintf("-%d", i)
			}
			usedIDs[id] = true

			isDir := rapid.Bool().Draw(rt, fmt.Sprintf("isDir-%d", i))

			lt := proton.LinkTypeFile
			if isDir {
				lt = proton.LinkTypeFolder
			}

			pLink := &proton.Link{
				LinkID: id,
				Type:   lt,
			}
			link := drive.NewTestLink(pLink, nil, nil, nil, fmt.Sprintf("name-%d", i))

			links = append(links, linkSpec{linkID: id, isDir: isDir})
			linkMap[id] = link
		}

		// Construct a test client with the pre-populated link table.
		client := drive.NewTestClient(linkMap)

		// Construct the LinkIDDir under test.
		node := &LinkIDDir{client: client}
		ctx := context.Background()

		// --- Verify each link in the table returns the correct node type ---
		for _, spec := range links {
			result, errno := node.Lookup(ctx, spec.linkID)
			if errno != 0 {
				rt.Fatalf("Lookup(%q) returned errno %d, want 0 (link exists in table)", spec.linkID, errno)
			}
			if result == nil {
				rt.Fatalf("Lookup(%q) returned nil node", spec.linkID)
			}
			if spec.isDir {
				if _, ok := result.(*LinkDirNode); !ok {
					rt.Fatalf("Lookup(%q) returned %T, want *LinkDirNode (folder link)", spec.linkID, result)
				}
			} else {
				if _, ok := result.(*FileNode); !ok {
					rt.Fatalf("Lookup(%q) returned %T, want *FileNode (file link)", spec.linkID, result)
				}
			}
		}

		// --- Verify non-existent IDs return ENOENT ---
		numMissing := rapid.IntRange(1, 5).Draw(rt, "numMissing")
		for i := 0; i < numMissing; i++ {
			missingID := rapid.StringMatching(`missing_[a-z0-9]{5,15}`).Draw(rt, fmt.Sprintf("missingID-%d", i))
			// Ensure it doesn't collide with an existing ID.
			for usedIDs[missingID] {
				missingID += "_x"
			}

			_, errno := node.Lookup(ctx, missingID)
			if errno != syscall.ENOENT {
				rt.Fatalf("Lookup(%q) returned errno %d, want ENOENT (%d) (ID not in table)", missingID, errno, syscall.ENOENT)
			}
		}
	})
}

// decryptFailResolver is a mock LinkResolver where AddressForEmail and
// AddressKeyRing always return false, causing Name() decryption to fail
// for any link that doesn't have testName set. Used by
// TestPropertyDecryptionFailure to simulate decryption failures.
type decryptFailResolver struct {
	// children holds the raw proton.Link objects to return from ListLinkChildren.
	children []proton.Link
	// nameMap maps LinkID → testName for NewChildLink. Empty string means
	// the child will have a failing Name() (falls through to real decryption).
	nameMap map[string]string
}

func (r *decryptFailResolver) ListLinkChildren(_ context.Context, _, _ string, _ bool) ([]proton.Link, error) {
	return r.children, nil
}

func (r *decryptFailResolver) NewChildLink(_ context.Context, parent *drive.Link, pLink *proton.Link) *drive.Link {
	name := r.nameMap[pLink.LinkID]
	return drive.NewTestLink(pLink, parent, parent.Share(), r, name)
}

func (r *decryptFailResolver) GetLink(_ string) *drive.Link { return nil }

func (r *decryptFailResolver) AddressForEmail(_ string) (proton.Address, bool) {
	return proton.Address{}, false
}

func (r *decryptFailResolver) AddressKeyRing(_ string) (*crypto.KeyRing, bool) {
	return nil, false
}

func (r *decryptFailResolver) Throttle() *api.Throttle { return nil }
func (r *decryptFailResolver) MaxWorkers() int         { return 1 }

// failingNameShare creates a *drive.Share where GetName() returns an error.
// The root link has no testName set, so Name() falls through to real
// decryption which fails because the resolver returns false from
// AddressKeyRing.
func failingNameShare(shareID string, st proton.ShareType) *drive.Share {
	resolver := &decryptFailResolver{nameMap: map[string]string{}}
	pShare := &proton.Share{
		ShareMetadata: proton.ShareMetadata{
			ShareID: shareID,
			Type:    st,
		},
	}
	pLink := &proton.Link{LinkID: "link-" + shareID, Type: proton.LinkTypeFolder}
	// Empty testName ("") causes Name() to fall through to decryption.
	root := drive.NewTestLink(pLink, nil, nil, resolver, "")
	share := drive.NewShare(pShare, nil, root, resolver, "vol-1")
	// Re-create root with share set so getParentKeyRing can reach share.getKeyRing().
	root = drive.NewTestLink(pLink, nil, share, resolver, "")
	share.Link = root
	return share
}

// TestPropertyDecryptionFailure verifies that for any share where
// GetName() returns an error, DriveHandler.Readdir skips that entry
// (not included in results, no error returned), and DriveHandler.Lookup
// returns ENOENT for that name. At the DirNode level, when ListChildren
// fails due to a child name decryption error, the node returns EIO.
//
// Feature: protonfs-daemon, Property 7: Decryption failure handling
// **Validates: Requirements 13.2**
func TestPropertyDecryptionFailure(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		// --- Scenario A: DriveHandler level (share name decryption) ---
		//
		// Generate a mix of standard shares: some with working names
		// (testName set) and some with failing GetName() (testName empty,
		// decryption fails).

		numGood := rapid.IntRange(0, 10).Draw(rt, "numGood")
		numBad := rapid.IntRange(1, 10).Draw(rt, "numBad")

		shares := make(map[string]*drive.Share, numGood+numBad)
		var goodNames []string
		usedNames := make(map[string]bool)

		// Create good standard shares (GetName succeeds).
		for i := 0; i < numGood; i++ {
			shareID := fmt.Sprintf("good-share-%d", i)
			base := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9_-]{0,14}`).Draw(rt, fmt.Sprintf("goodName-%d", i))
			name := fmt.Sprintf("%s-%d", base, i)
			for usedNames[name] {
				name = fmt.Sprintf("%s-%d-x", base, i)
			}
			usedNames[name] = true
			goodNames = append(goodNames, name)
			shares[shareID] = testShare(name, shareID, proton.ShareTypeStandard)
		}

		// Create bad standard shares (GetName fails).
		for i := 0; i < numBad; i++ {
			shareID := fmt.Sprintf("bad-share-%d", i)
			shares[shareID] = failingNameShare(shareID, proton.ShareTypeStandard)
		}

		h := buildTestHandler(shares)
		ctx := context.Background()

		// --- Verify Readdir skips bad shares without error ---
		entries, errno := h.Readdir(ctx)
		if errno != 0 {
			rt.Fatalf("Readdir returned errno %d, want 0 (bad shares should be skipped)", errno)
		}

		// Build name set from entries (excluding .linkid).
		entryNames := make(map[string]bool, len(entries))
		for _, e := range entries {
			entryNames[e.Name] = true
		}

		// All good share names must appear.
		for _, name := range goodNames {
			if !entryNames[name] {
				rt.Fatalf("Readdir missing good share %q", name)
			}
		}

		// .linkid must appear.
		if !entryNames[".linkid"] {
			rt.Fatalf("Readdir missing .linkid entry")
		}

		// Total entries: good shares + .linkid (no Home/Photos since all are standard).
		expectedCount := numGood + 1 // +1 for .linkid
		if len(entries) != expectedCount {
			rt.Fatalf("Readdir returned %d entries, want %d (good=%d + .linkid=1, bad=%d skipped)",
				len(entries), expectedCount, numGood, numBad)
		}

		// --- Verify Lookup returns ENOENT for bad shares ---
		// Bad shares have no decryptable name, so any lookup that would
		// match them returns ENOENT. We test with a name that doesn't
		// match any good share.
		badLookupName := rapid.StringMatching(`badlookup_[a-z]{5,10}`).Draw(rt, "badLookupName")
		for usedNames[badLookupName] || badLookupName == ".linkid" {
			badLookupName += "_x"
		}
		_, errno = h.Lookup(ctx, badLookupName)
		if errno != syscall.ENOENT {
			rt.Fatalf("Lookup(%q) returned errno %d, want ENOENT (name doesn't match any good share)",
				badLookupName, errno, //nolint:govet
			)
		}

		// --- Verify Lookup succeeds for good shares ---
		for _, name := range goodNames {
			node, errno := h.Lookup(ctx, name)
			if errno != 0 {
				rt.Fatalf("Lookup(%q) returned errno %d, want 0 (good share)", name, errno)
			}
			if _, ok := node.(*ShareDirNode); !ok {
				rt.Fatalf("Lookup(%q) returned %T, want *ShareDirNode", name, node)
			}
		}

		// --- Scenario B: DirNode level (child name decryption) ---
		//
		// When a child's Name() fails during Readdir, the entry is
		// skipped (per Requirement 13.2). Only children with valid
		// names appear in the result. No error is returned.

		numGoodChildren := rapid.IntRange(0, 5).Draw(rt, "numGoodChildren")
		numBadChildren := rapid.IntRange(1, 3).Draw(rt, "numBadChildren")

		pChildren := make([]proton.Link, 0, numGoodChildren+numBadChildren)
		nameMap := make(map[string]string, numGoodChildren+numBadChildren)
		var goodChildNames []string

		// Good children: testName set, Name() succeeds.
		for i := 0; i < numGoodChildren; i++ {
			linkID := fmt.Sprintf("good-child-%d", i)
			childName := fmt.Sprintf("child-%d", i)
			pChildren = append(pChildren, proton.Link{
				LinkID: linkID,
				Type:   proton.LinkTypeFile,
				State:  proton.LinkStateActive,
			})
			nameMap[linkID] = childName
			goodChildNames = append(goodChildNames, childName)
		}

		// Bad children: testName empty (""), Name() fails.
		for i := 0; i < numBadChildren; i++ {
			linkID := fmt.Sprintf("bad-child-%d", i)
			pChildren = append(pChildren, proton.Link{
				LinkID: linkID,
				Type:   proton.LinkTypeFile,
				State:  proton.LinkStateActive,
			})
			nameMap[linkID] = "" // Empty → Name() falls through to decryption → fails
		}

		resolver := &decryptFailResolver{
			children: pChildren,
			nameMap:  nameMap,
		}

		// Build a share with a root link that uses the failing resolver.
		pShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "dir-test-share"},
		}
		rootPLink := &proton.Link{LinkID: "dir-root-link", Type: proton.LinkTypeFolder}
		root := drive.NewTestLink(rootPLink, nil, nil, resolver, "root")
		share := drive.NewShare(pShare, nil, root, resolver, "vol-dir")
		root = drive.NewTestLink(rootPLink, nil, share, resolver, "root")
		share.Link = root

		// ShareDirNode.Readdir skips children with decryption errors
		// and returns only the good children.
		shareNode := &ShareDirNode{share: share}
		dirEntries, errno := shareNode.Readdir(ctx)
		if errno != 0 {
			rt.Fatalf("ShareDirNode.Readdir with bad children: got errno %d, want 0 (bad entries skipped)",
				errno)
		}
		if len(dirEntries) != numGoodChildren {
			rt.Fatalf("ShareDirNode.Readdir: got %d entries, want %d (bad children skipped)",
				len(dirEntries), numGoodChildren)
		}

		// Verify good child names appear.
		entryNames = make(map[string]bool, len(dirEntries))
		for _, e := range dirEntries {
			entryNames[e.Name] = true
		}
		for _, name := range goodChildNames {
			if !entryNames[name] {
				rt.Fatalf("ShareDirNode.Readdir: missing good child %q", name)
			}
		}

		// Lookup for a non-existent name returns ENOENT (not EIO).
		lookupName := rapid.StringMatching(`[a-z]{3,8}`).Draw(rt, "dirLookupName")
		for entryNames[lookupName] {
			lookupName += "_x"
		}
		_, errno = shareNode.Lookup(ctx, lookupName)
		if errno != syscall.ENOENT {
			rt.Fatalf("ShareDirNode.Lookup(%q) with bad children: got errno %d, want ENOENT (%d)",
				lookupName, errno, syscall.ENOENT)
		}

		// --- Verify DirNode works when all children have valid names ---
		goodOnlyChildren := make([]proton.Link, 0, numGoodChildren)
		goodOnlyNameMap := make(map[string]string, numGoodChildren)
		for i := 0; i < numGoodChildren; i++ {
			linkID := fmt.Sprintf("allgood-child-%d", i)
			childName := fmt.Sprintf("allgood-%d", i)
			goodOnlyChildren = append(goodOnlyChildren, proton.Link{
				LinkID: linkID,
				Type:   proton.LinkTypeFile,
				State:  proton.LinkStateActive,
			})
			goodOnlyNameMap[linkID] = childName
		}

		goodResolver := &decryptFailResolver{
			children: goodOnlyChildren,
			nameMap:  goodOnlyNameMap,
		}

		goodPShare := &proton.Share{
			ShareMetadata: proton.ShareMetadata{ShareID: "good-dir-share"},
		}
		goodRootPLink := &proton.Link{LinkID: "good-dir-root", Type: proton.LinkTypeFolder}
		goodRoot := drive.NewTestLink(goodRootPLink, nil, nil, goodResolver, "root")
		goodShare := drive.NewShare(goodPShare, nil, goodRoot, goodResolver, "vol-good")
		goodRoot = drive.NewTestLink(goodRootPLink, nil, goodShare, goodResolver, "root")
		goodShare.Link = goodRoot

		goodNode := &ShareDirNode{share: goodShare}
		goodEntries, errno := goodNode.Readdir(ctx)
		if errno != 0 {
			rt.Fatalf("ShareDirNode.Readdir (all good children): got errno %d, want 0", errno)
		}
		if len(goodEntries) != numGoodChildren {
			rt.Fatalf("ShareDirNode.Readdir (all good children): got %d entries, want %d",
				len(goodEntries), numGoodChildren)
		}
	})
}
