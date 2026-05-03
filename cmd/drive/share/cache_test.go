package shareCmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/config"
	"github.com/major0/proton-cli/api/drive"
	"pgregory.net/rapid"
)

func TestProhibitedShareType(t *testing.T) {
	prohibited := []proton.ShareType{proton.ShareTypeMain, drive.ShareTypePhotos, proton.ShareTypeDevice}
	for _, st := range prohibited {
		if !prohibitedShareType(st) {
			t.Errorf("expected share type %d to be prohibited", st)
		}
	}

	if prohibitedShareType(proton.ShareTypeStandard) {
		t.Error("ShareTypeStandard should be allowed")
	}
}

// TestShareCacheToggleRoundTrip_Property verifies that toggling cache
// settings via config save/load produces the expected state.
//
// **Property 5: Share cache toggle round-trip**
// **Validates: Requirements 4.1, 4.2**
func TestShareCacheToggleRoundTrip_Property(t *testing.T) {
	dir := t.TempDir()

	memoryLevelGen := rapid.SampledFrom([]api.MemoryCacheLevel{api.CacheDisabled, api.CacheLinkName, api.CacheMetadata})
	diskLevelGen := rapid.SampledFrom([]api.DiskCacheLevel{api.DiskCacheDisabled, api.DiskCacheObjectStore})

	rapid.Check(t, func(t *rapid.T) {
		name := rapid.StringMatching(`[a-zA-Z][a-zA-Z0-9 ]{2,15}`).Draw(t, "name")
		memory := memoryLevelGen.Draw(t, "memory")
		disk := diskLevelGen.Draw(t, "disk")

		cfg := config.DefaultConfig()
		cfg.Shares[name] = api.ShareConfig{
			MemoryCache: memory,
			DiskCache:   disk,
		}

		path := filepath.Join(dir, rapid.StringMatching(`[a-z]{8}`).Draw(t, "file")+".yaml")
		if err := config.SaveConfig(path, cfg); err != nil {
			t.Fatalf("SaveConfig: %v", err)
		}

		loaded, err := config.LoadConfig(path)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		sc := loaded.Shares[name]
		if sc.MemoryCache != memory {
			t.Fatalf("memory: got %v, want %v", sc.MemoryCache, memory)
		}
		if sc.DiskCache != disk {
			t.Fatalf("disk: got %v, want %v", sc.DiskCache, disk)
		}
	})
}

// TestPrintCacheState verifies the output format of printCacheState.
func TestPrintCacheState(t *testing.T) {
	tests := []struct {
		name     string
		share    string
		sc       api.ShareConfig
		wantSubs []string
	}{
		{
			"all disabled",
			"test-share",
			api.ShareConfig{},
			[]string{"Share: test-share", "memory:   disabled", "disk:     disabled"},
		},
		{
			"metadata/objectstore",
			"my-share",
			api.ShareConfig{MemoryCache: api.CacheMetadata, DiskCache: api.DiskCacheObjectStore},
			[]string{"Share: my-share", "memory:   metadata", "disk:     objectstore"},
		},
		{
			"linkname/disabled",
			"mixed",
			api.ShareConfig{MemoryCache: api.CacheLinkName, DiskCache: api.DiskCacheDisabled},
			[]string{"memory:   linkname", "disk:     disabled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture stdout.
			old := os.Stdout
			r, w, _ := os.Pipe()
			os.Stdout = w

			printCacheState(tt.share, tt.sc)

			_ = w.Close()
			os.Stdout = old

			var buf bytes.Buffer
			_, _ = io.Copy(&buf, r)
			got := buf.String()

			for _, sub := range tt.wantSubs {
				if !strings.Contains(got, sub) {
					t.Errorf("output missing %q, got:\n%s", sub, got)
				}
			}
		})
	}
}

// TestShareCacheCmd_RestoreError verifies that runShareCache returns
// an error when session restore fails.
func TestShareCacheCmd_RestoreError(t *testing.T) {
	saveAndRestore(t)
	injectSessionError(fmt.Errorf("keyring locked"))

	err := shareCacheCmd.RunE(shareCacheCmd, []string{"myshare"})
	if err == nil || !strings.Contains(err.Error(), "keyring locked") {
		t.Fatalf("error = %v, want 'keyring locked'", err)
	}
}
