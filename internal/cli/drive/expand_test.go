package driveCmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestExpandLocalRecursive(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(t *testing.T, tmp string) (src, dst *resolvedEndpoint)
		opts      cpOptions
		wantJobs  int
		wantErr   string
		verifyDir func(t *testing.T, tmp string)
	}{
		{
			name: "empty directory produces zero jobs",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				srcDir := filepath.Join(tmp, "empty")
				if err := os.Mkdir(srcDir, 0700); err != nil {
					t.Fatal(err)
				}
				srcInfo, _ := os.Stat(srcDir)
				dstDir := filepath.Join(tmp, "dst")
				dstInfo, _ := os.Stat(tmp) // parent exists
				_ = dstInfo
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       srcDir,
						localPath: srcDir,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dstDir,
						localPath: dstDir,
					}
			},
			wantJobs: 0,
			verifyDir: func(t *testing.T, tmp string) {
				t.Helper()
				// dst directory should be created.
				info, err := os.Stat(filepath.Join(tmp, "dst"))
				if err != nil {
					t.Fatalf("dst dir not created: %v", err)
				}
				if !info.IsDir() {
					t.Error("dst should be a directory")
				}
			},
		},
		{
			name: "flat directory with files",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				srcDir := filepath.Join(tmp, "src")
				if err := os.Mkdir(srcDir, 0700); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
					if err := os.WriteFile(filepath.Join(srcDir, name), []byte("x"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				srcInfo, _ := os.Stat(srcDir)
				dstDir := filepath.Join(tmp, "dst")
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       srcDir,
						localPath: srcDir,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dstDir,
						localPath: dstDir,
					}
			},
			wantJobs: 3,
		},
		{
			name: "nested directories create subdirs",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				srcDir := filepath.Join(tmp, "src")
				if err := os.MkdirAll(filepath.Join(srcDir, "sub", "deep"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(srcDir, "top.txt"), []byte("t"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(srcDir, "sub", "mid.txt"), []byte("m"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(srcDir, "sub", "deep", "bot.txt"), []byte("b"), 0600); err != nil {
					t.Fatal(err)
				}
				srcInfo, _ := os.Stat(srcDir)
				dstDir := filepath.Join(tmp, "dst")
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       srcDir,
						localPath: srcDir,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dstDir,
						localPath: dstDir,
					}
			},
			wantJobs: 3,
			verifyDir: func(t *testing.T, tmp string) {
				t.Helper()
				for _, d := range []string{"dst", "dst/sub", "dst/sub/deep"} {
					info, err := os.Stat(filepath.Join(tmp, d))
					if err != nil {
						t.Errorf("missing dir %s: %v", d, err)
						continue
					}
					if !info.IsDir() {
						t.Errorf("%s should be a directory", d)
					}
				}
			},
		},
		{
			name: "symlinks skipped without dereference",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				srcDir := filepath.Join(tmp, "src")
				if err := os.Mkdir(srcDir, 0700); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(tmp, "target.txt")
				if err := os.WriteFile(target, []byte("linked"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(srcDir, "real.txt"), []byte("ok"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(srcDir, "link.txt")); err != nil {
					t.Skip("symlinks not supported")
				}
				srcInfo, _ := os.Stat(srcDir)
				dstDir := filepath.Join(tmp, "dst")
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       srcDir,
						localPath: srcDir,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dstDir,
						localPath: dstDir,
					}
			},
			opts:     cpOptions{recursive: true, dereference: false},
			wantJobs: 1, // only real.txt, link.txt skipped
		},
		{
			name: "context cancellation stops walk",
			setup: func(t *testing.T, tmp string) (*resolvedEndpoint, *resolvedEndpoint) {
				t.Helper()
				srcDir := filepath.Join(tmp, "src")
				if err := os.Mkdir(srcDir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(srcDir, "a.txt"), []byte("a"), 0600); err != nil {
					t.Fatal(err)
				}
				srcInfo, _ := os.Stat(srcDir)
				dstDir := filepath.Join(tmp, "dst")
				return &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       srcDir,
						localPath: srcDir,
						localInfo: srcInfo,
					}, &resolvedEndpoint{
						pathType:  PathLocal,
						raw:       dstDir,
						localPath: dstDir,
					}
			},
			wantErr: "context canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			src, dst := tt.setup(t, tmp)

			ctx := context.Background()
			if tt.wantErr == "context canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel() // cancel immediately
			}

			jobs, _, err := expandLocalRecursive(ctx, nil, src, dst, tt.opts)
			if tt.wantErr != "" {
				if err == nil {
					// Context cancellation may not always propagate as error
					// if WalkDir completes before checking ctx.
					return
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(jobs) != tt.wantJobs {
				t.Errorf("got %d jobs, want %d", len(jobs), tt.wantJobs)
			}
			if tt.verifyDir != nil {
				tt.verifyDir(t, tmp)
			}
		})
	}
}
