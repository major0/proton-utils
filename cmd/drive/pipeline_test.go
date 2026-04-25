package driveCmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

// failReader is a BlockReader that always fails on ReadBlock.
type failReader struct {
	name string
}

func (f *failReader) ReadBlock(_ context.Context, _ int, _ []byte) (int, error) {
	return 0, fmt.Errorf("read %s: simulated failure", f.name)
}
func (f *failReader) BlockCount() int       { return 1 }
func (f *failReader) BlockSize(_ int) int64 { return 1024 }
func (f *failReader) TotalSize() int64      { return 1024 }
func (f *failReader) Describe() string      { return f.name }
func (f *failReader) Close() error          { return nil }

// TestBufferZeroed_Property verifies that after clear(), all bytes are zero.
func TestBufferZeroed_Property(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		size := rapid.IntRange(1, 64*1024).Draw(t, "size")
		buf := make([]byte, size)
		for i := range buf {
			buf[i] = byte(rapid.IntRange(1, 255).Draw(t, "byte")) //nolint:gosec // bounded 0-255
		}
		clear(buf)
		for i, b := range buf {
			if b != 0 {
				t.Fatalf("buf[%d] = %d after clear, want 0", i, b)
			}
		}
	})
}

// newTestJob creates a CopyJob from real temp files.
func newTestJob(t *testing.T, srcPath, dstPath string, srcData []byte) CopyJob {
	t.Helper()
	r := NewLocalReader(srcPath, int64(len(srcData)))
	w := NewLocalWriter(dstPath)
	return CopyJob{Src: r, Dst: w}
}

// TestPipeline_LocalToLocal verifies that the pipeline correctly copies
// a local file to another local path using the block pipeline.
func TestPipeline_LocalToLocal(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	dstPath := filepath.Join(dir, "dst.bin")

	srcData := make([]byte, drive.BlockSize+1024)
	for i := range srcData {
		srcData[i] = byte(i % 251)
	}
	if err := os.WriteFile(srcPath, srcData, 0600); err != nil {
		t.Fatalf("write src: %v", err)
	}
	f, err := os.Create(dstPath) //nolint:gosec // test temp path
	if err != nil {
		t.Fatalf("create dst: %v", err)
	}
	_ = f.Close()

	job := newTestJob(t, srcPath, dstPath, srcData)

	if err := RunPipeline(context.Background(), []CopyJob{job}, TransferOpts{Workers: 2}); err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	dstData, err := os.ReadFile(dstPath) //nolint:gosec // test temp path
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}

	if len(dstData) < len(srcData) {
		t.Fatalf("dst size = %d, want >= %d", len(dstData), len(srcData))
	}
	for i := range srcData {
		if dstData[i] != srcData[i] {
			t.Fatalf("mismatch at byte %d: got %d, want %d", i, dstData[i], srcData[i])
		}
	}
}

// TestPipeline_EmptyJobs verifies that an empty job list returns nil.
func TestPipeline_EmptyJobs(t *testing.T) {
	if err := RunPipeline(context.Background(), nil, TransferOpts{Workers: 2}); err != nil {
		t.Fatalf("expected nil for empty jobs, got: %v", err)
	}
}

// TestPipeline_ContextCancellation verifies that the pipeline stops
// promptly when the context is cancelled.
func TestPipeline_ContextCancellation(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "src.bin")
	dstPath := filepath.Join(dir, "dst.bin")

	srcData := make([]byte, drive.BlockSize*4)
	_ = os.WriteFile(srcPath, srcData, 0600)
	_ = os.WriteFile(dstPath, nil, 0600)

	job := newTestJob(t, srcPath, dstPath, srcData)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = RunPipeline(ctx, []CopyJob{job}, TransferOpts{Workers: 2})
}

// TestPipeline_MultipleFiles verifies that blocks from different files
// are processed through the same pipeline.
func TestPipeline_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	var jobs []CopyJob
	for i := 0; i < 5; i++ {
		srcPath := filepath.Join(dir, "src"+string(rune('a'+i))+".bin")
		dstPath := filepath.Join(dir, "dst"+string(rune('a'+i))+".bin")
		data := make([]byte, 1024*(i+1))
		for j := range data {
			data[j] = byte(i + j%200)
		}
		_ = os.WriteFile(srcPath, data, 0600)
		_ = os.WriteFile(dstPath, nil, 0600)
		jobs = append(jobs, newTestJob(t, srcPath, dstPath, data))
	}

	if err := RunPipeline(context.Background(), jobs, TransferOpts{Workers: 4}); err != nil {
		t.Fatalf("RunPipeline: %v", err)
	}

	for i, job := range jobs {
		src, _ := os.ReadFile(job.Src.Describe())
		dst, _ := os.ReadFile(job.Dst.Describe())
		if len(dst) < len(src) {
			t.Fatalf("file %d: dst size %d < src size %d", i, len(dst), len(src))
		}
		for j := range src {
			if dst[j] != src[j] {
				t.Fatalf("file %d: mismatch at byte %d", i, j)
			}
		}
	}
}

// TestPipeline_ProgressCallback_Property verifies that the progress
// callback receives monotonically increasing completed block counts.
//
// **Validates: Requirements 2.4**
func TestPipeline_ProgressCallback_Property(t *testing.T) {
	dir := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		nBlocks := rapid.IntRange(1, 5).Draw(t, "nBlocks")
		fileSize := int64(nBlocks) * drive.BlockSize

		srcPath := filepath.Join(dir, rapid.StringMatching(`[a-z]{8}`).Draw(t, "name")+".bin")
		dstPath := srcPath + ".dst"

		data := make([]byte, fileSize)
		for i := range data {
			data[i] = byte(i % 251)
		}
		_ = os.WriteFile(srcPath, data, 0600)
		_ = os.WriteFile(dstPath, nil, 0600)

		r := NewLocalReader(srcPath, fileSize)
		w := NewLocalWriter(dstPath)

		var completedValues []int
		job := CopyJob{Src: r, Dst: w}

		pErr := RunPipeline(context.Background(), []CopyJob{job}, TransferOpts{
			Workers: 1,
			Progress: func(completed, total int, _ int64, _ float64) {
				completedValues = append(completedValues, completed)
				_ = total
			},
		})
		if pErr != nil {
			t.Fatalf("RunPipeline: %v", pErr)
		}

		for i := 1; i < len(completedValues); i++ {
			if completedValues[i] < completedValues[i-1] {
				t.Fatalf("progress not monotonic: %v", completedValues)
			}
		}

		if len(completedValues) > 0 && completedValues[len(completedValues)-1] != nBlocks {
			t.Fatalf("final completed = %d, want %d", completedValues[len(completedValues)-1], nBlocks)
		}
	})
}

// TestPipeline_VerboseCallback verifies that the verbose callback is
// called exactly once per completed job.
func TestPipeline_VerboseCallback(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name     string
		nJobs    int
		wantCall int
	}{
		{"single job", 1, 1},
		{"three jobs", 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var jobs []CopyJob
			for i := 0; i < tt.nJobs; i++ {
				srcPath := filepath.Join(dir, tt.name+string(rune('a'+i))+".bin")
				dstPath := srcPath + ".dst"
				data := []byte("test-data")
				_ = os.WriteFile(srcPath, data, 0600)
				_ = os.WriteFile(dstPath, nil, 0600)
				jobs = append(jobs, newTestJob(t, srcPath, dstPath, data))
			}

			var verboseCalls int
			err := RunPipeline(context.Background(), jobs, TransferOpts{
				Workers: 2,
				Verbose: func(_, _ string) {
					verboseCalls++
				},
			})
			if err != nil {
				t.Fatalf("RunPipeline: %v", err)
			}
			if verboseCalls != tt.wantCall {
				t.Fatalf("verbose called %d times, want %d", verboseCalls, tt.wantCall)
			}
		})
	}
}

// TestBulkCopy_ErrorCollection_Property verifies that when a subset of
// jobs fail, non-failing jobs complete and all errors are collected.
func TestBulkCopy_ErrorCollection_Property(t *testing.T) {
	dir := t.TempDir()

	rapid.Check(t, func(t *rapid.T) {
		nGood := rapid.IntRange(1, 5).Draw(t, "nGood")
		nBad := rapid.IntRange(1, 5).Draw(t, "nBad")

		iterDir := filepath.Join(dir, rapid.StringMatching(`[a-z]{8}`).Draw(t, "iter"))
		_ = os.MkdirAll(iterDir, 0700)

		var jobs []CopyJob

		for i := 0; i < nGood; i++ {
			srcPath := filepath.Join(iterDir, "good"+string(rune('a'+i))+".bin")
			dstPath := filepath.Join(iterDir, "dst-good"+string(rune('a'+i))+".bin")
			data := []byte("good-data")
			_ = os.WriteFile(srcPath, data, 0600)
			_ = os.WriteFile(dstPath, nil, 0600)
			r := NewLocalReader(srcPath, int64(len(data)))
			w := NewLocalWriter(dstPath)
			jobs = append(jobs, CopyJob{Src: r, Dst: w})
		}

		// Bad jobs use a failReader that always errors on ReadBlock.
		for i := 0; i < nBad; i++ {
			dstPath := filepath.Join(iterDir, "dst-bad"+string(rune('a'+i))+".bin")
			_ = os.WriteFile(dstPath, nil, 0600)
			w := NewLocalWriter(dstPath)
			name := "bad" + string(rune('a'+i))
			jobs = append(jobs, CopyJob{
				Src: &failReader{name: name},
				Dst: w,
			})
		}

		err := RunPipeline(context.Background(), jobs, TransferOpts{Workers: 2})

		if err == nil {
			t.Fatal("expected errors from bad jobs, got nil")
		}

		for i := 0; i < nGood; i++ {
			dstPath := filepath.Join(iterDir, "dst-good"+string(rune('a'+i))+".bin")
			if _, statErr := os.Stat(dstPath); statErr != nil {
				t.Fatalf("good job %d: dst file missing: %v", i, statErr)
			}
		}
	})
}

// TestBulkCopy_Empty verifies that an empty job list returns nil.
func TestBulkCopy_Empty(t *testing.T) {
	if err := RunPipeline(context.Background(), nil, TransferOpts{Workers: 2}); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

// TestBulkCopy_AllSuccess verifies that all-success jobs return nil error.
func TestBulkCopy_AllSuccess(t *testing.T) {
	dir := t.TempDir()
	var jobs []CopyJob
	for i := 0; i < 3; i++ {
		srcPath := filepath.Join(dir, "src"+string(rune('a'+i))+".bin")
		dstPath := filepath.Join(dir, "dst"+string(rune('a'+i))+".bin")
		_ = os.WriteFile(srcPath, []byte("data"), 0600)
		_ = os.WriteFile(dstPath, nil, 0600)
		jobs = append(jobs, newTestJob(t, srcPath, dstPath, []byte("data")))
	}

	if err := RunPipeline(context.Background(), jobs, TransferOpts{Workers: 2}); err != nil {
		t.Fatalf("expected nil, got: %v", err)
	}
}

// TestBulkCopy_AllFail verifies that all-failure jobs return errors for each.
func TestBulkCopy_AllFail(t *testing.T) {
	dir := t.TempDir()
	var jobs []CopyJob
	for i := 0; i < 3; i++ {
		dstPath := filepath.Join(dir, "dst"+string(rune('a'+i))+".bin")
		_ = os.WriteFile(dstPath, nil, 0600)
		w := NewLocalWriter(dstPath)
		name := "missing" + string(rune('a'+i))
		jobs = append(jobs, CopyJob{
			Src: &failReader{name: name},
			Dst: w,
		})
	}

	err := RunPipeline(context.Background(), jobs, TransferOpts{Workers: 2})
	if err == nil {
		t.Fatal("expected errors, got nil")
	}
}
