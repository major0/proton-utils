package driveCmd

import (
	"fmt"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api/drive"
	"pgregory.net/rapid"
)

// makeTestEntry creates a listEntry with a file-type Link that returns the
// given size via Size() and mtime via ModifyTime() (using fallback values
// since no resolver/share are provided). The link has no active revision ID,
// so ensureXAttr is a no-op and the fallback path returns ActiveRevision.Size
// and ActiveRevision.CreateTime respectively.
func makeTestEntry(name string, size int64, mtime int64) listEntry {
	pl := &proton.Link{
		LinkID: name + "-id",
		Type:   proton.LinkTypeFile,
		State:  proton.LinkStateActive,
		FileProperties: &proton.FileProperties{
			ActiveRevision: proton.RevisionMetadata{
				Size:       size,
				CreateTime: mtime,
			},
		},
	}
	l := drive.NewTestLink(pl, nil, nil, nil, name)
	return listEntry{entry: drive.DirEntry{Link: l}, name: name}
}

// TestPropertySortCorrectness verifies that sortEntries produces descending
// order for both size and time sort modes when all values are distinct.
//
// **Property 11: Sort Correctness**
// **Validates: Requirements 3.5, 10.1, 10.2**
func TestPropertySortCorrectness(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(2, 50).Draw(rt, "n")

		// Generate distinct size values.
		sizeSet := make(map[int64]bool, n)
		sizes := make([]int64, 0, n)
		for len(sizes) < n {
			v := rapid.Int64Range(0, 1<<40).Draw(rt, fmt.Sprintf("size[%d]", len(sizes)))
			if !sizeSet[v] {
				sizeSet[v] = true
				sizes = append(sizes, v)
			}
		}

		// Generate distinct mtime values.
		mtimeSet := make(map[int64]bool, n)
		mtimes := make([]int64, 0, n)
		for len(mtimes) < n {
			v := rapid.Int64Range(0, 1<<34).Draw(rt, fmt.Sprintf("mtime[%d]", len(mtimes)))
			if !mtimeSet[v] {
				mtimeSet[v] = true
				mtimes = append(mtimes, v)
			}
		}

		// Build entries with distinct sizes and mtimes.
		entries := make([]listEntry, n)
		for i := 0; i < n; i++ {
			entries[i] = makeTestEntry(fmt.Sprintf("f%d", i), sizes[i], mtimes[i])
		}

		// Test sort by size — expect descending order (largest first).
		sizeEntries := make([]listEntry, n)
		copy(sizeEntries, entries)
		sortEntries(sizeEntries, listOpts{sortBy: sortSize})

		for i := 1; i < n; i++ {
			prev := sizeEntries[i-1].entry.Link.Size()
			curr := sizeEntries[i].entry.Link.Size()
			if prev < curr {
				rt.Fatalf("sort by size not descending at index %d: %d < %d", i, prev, curr)
			}
		}

		// Test sort by time — expect descending order (newest first).
		timeEntries := make([]listEntry, n)
		copy(timeEntries, entries)
		sortEntries(timeEntries, listOpts{sortBy: sortTime})

		for i := 1; i < n; i++ {
			prev := timeEntries[i-1].entry.Link.ModifyTime()
			curr := timeEntries[i].entry.Link.ModifyTime()
			if prev < curr {
				rt.Fatalf("sort by time not descending at index %d: %d < %d", i, prev, curr)
			}
		}
	})
}

// TestPropertyStableSortPreservesOrder verifies that when multiple entries
// have equal sort keys, the sort preserves their original relative order.
//
// **Property 12: Stable Sort Preserves Order for Equal Keys**
// **Validates: Requirements 10.3**
func TestPropertyStableSortPreservesOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(3, 50).Draw(rt, "n")

		// Pick a shared size and shared mtime that multiple entries will share.
		sharedSize := rapid.Int64Range(1, 1<<30).Draw(rt, "sharedSize")
		sharedMtime := rapid.Int64Range(1000000, 1<<34).Draw(rt, "sharedMtime")

		// Decide how many entries get the shared value (at least 2).
		nDup := rapid.IntRange(2, n).Draw(rt, "nDup")

		// Assign sizes: first nDup entries get sharedSize, rest get distinct values.
		sizes := make([]int64, n)
		for i := 0; i < nDup; i++ {
			sizes[i] = sharedSize
		}
		usedSizes := map[int64]bool{sharedSize: true}
		for i := nDup; i < n; i++ {
			for {
				v := rapid.Int64Range(0, 1<<40).Draw(rt, fmt.Sprintf("usize[%d]", i))
				if !usedSizes[v] {
					usedSizes[v] = true
					sizes[i] = v
					break
				}
			}
		}

		// Assign mtimes: first nDup entries get sharedMtime, rest get distinct values.
		mtimes := make([]int64, n)
		for i := 0; i < nDup; i++ {
			mtimes[i] = sharedMtime
		}
		usedMtimes := map[int64]bool{sharedMtime: true}
		for i := nDup; i < n; i++ {
			for {
				v := rapid.Int64Range(0, 1<<34).Draw(rt, fmt.Sprintf("umtime[%d]", i))
				if !usedMtimes[v] {
					usedMtimes[v] = true
					mtimes[i] = v
					break
				}
			}
		}

		// Build entries and shuffle (rapid draws a permutation).
		entries := make([]listEntry, n)
		for i := 0; i < n; i++ {
			entries[i] = makeTestEntry(fmt.Sprintf("f%d", i), sizes[i], mtimes[i])
		}

		// Shuffle entries using rapid-drawn permutation.
		perm := rapid.SliceOfN(rapid.IntRange(0, n-1), n, n).Draw(rt, "perm_source")
		// Fisher-Yates via rapid draws for a proper permutation.
		shuffled := make([]listEntry, n)
		copy(shuffled, entries)
		for i := n - 1; i > 0; i-- {
			j := perm[i] % (i + 1)
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		}

		// Record original indices for entries with equal keys.
		// Test sort by size stability.
		sizeEntries := make([]listEntry, n)
		copy(sizeEntries, shuffled)

		// Record original positions of entries with sharedSize.
		origSizeOrder := make([]string, 0)
		for _, e := range sizeEntries {
			if e.entry.Link.Size() == sharedSize {
				origSizeOrder = append(origSizeOrder, e.name)
			}
		}

		sortEntries(sizeEntries, listOpts{sortBy: sortSize})

		// Verify entries with sharedSize maintain relative order.
		sortedSizeOrder := make([]string, 0)
		for _, e := range sizeEntries {
			if e.entry.Link.Size() == sharedSize {
				sortedSizeOrder = append(sortedSizeOrder, e.name)
			}
		}
		if len(origSizeOrder) != len(sortedSizeOrder) {
			rt.Fatalf("lost entries with shared size: orig=%d sorted=%d", len(origSizeOrder), len(sortedSizeOrder))
		}
		for i := range origSizeOrder {
			if origSizeOrder[i] != sortedSizeOrder[i] {
				rt.Fatalf("stable sort by size violated at position %d: orig=%v sorted=%v",
					i, origSizeOrder, sortedSizeOrder)
			}
		}

		// Test sort by time stability.
		timeEntries := make([]listEntry, n)
		copy(timeEntries, shuffled)

		origTimeOrder := make([]string, 0)
		for _, e := range timeEntries {
			if e.entry.Link.ModifyTime() == sharedMtime {
				origTimeOrder = append(origTimeOrder, e.name)
			}
		}

		sortEntries(timeEntries, listOpts{sortBy: sortTime})

		sortedTimeOrder := make([]string, 0)
		for _, e := range timeEntries {
			if e.entry.Link.ModifyTime() == sharedMtime {
				sortedTimeOrder = append(sortedTimeOrder, e.name)
			}
		}
		if len(origTimeOrder) != len(sortedTimeOrder) {
			rt.Fatalf("lost entries with shared mtime: orig=%d sorted=%d", len(origTimeOrder), len(sortedTimeOrder))
		}
		for i := range origTimeOrder {
			if origTimeOrder[i] != sortedTimeOrder[i] {
				rt.Fatalf("stable sort by time violated at position %d: orig=%v sorted=%v",
					i, origTimeOrder, sortedTimeOrder)
			}
		}
	})
}

// TestPropertyReverseFlagInvertsSortOrder verifies that sorting with the
// reverse flag produces the exact reverse of the non-reversed sort.
//
// **Property 13: Reverse Flag Inverts Sort Order**
// **Validates: Requirements 10.4**
func TestPropertyReverseFlagInvertsSortOrder(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(2, 50).Draw(rt, "n")

		// Generate entries with random sizes and mtimes.
		entries := make([]listEntry, n)
		for i := 0; i < n; i++ {
			size := rapid.Int64Range(0, 1<<40).Draw(rt, fmt.Sprintf("size[%d]", i))
			mtime := rapid.Int64Range(0, 1<<34).Draw(rt, fmt.Sprintf("mtime[%d]", i))
			entries[i] = makeTestEntry(fmt.Sprintf("f%d", i), size, mtime)
		}

		// Test both sort modes.
		for _, mode := range []sortMode{sortSize, sortTime} {
			modeName := "size"
			if mode == sortTime {
				modeName = "time"
			}

			// Sort without reverse.
			normal := make([]listEntry, n)
			copy(normal, entries)
			sortEntries(normal, listOpts{sortBy: mode})

			// Sort with reverse.
			reversed := make([]listEntry, n)
			copy(reversed, entries)
			sortEntries(reversed, listOpts{sortBy: mode, reverse: true})

			// Verify reversed is the exact reverse of normal.
			for i := 0; i < n; i++ {
				j := n - 1 - i
				if normal[i].name != reversed[j].name {
					rt.Fatalf("reverse flag did not invert sort by %s at index %d: "+
						"normal[%d]=%s reversed[%d]=%s",
						modeName, i, i, normal[i].name, j, reversed[j].name)
				}
			}
		}
	})
}
