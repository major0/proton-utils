//go:build linux

// Command proton-fuse mounts the per-user Proton FUSE filesystem.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ProtonMail/go-proton-api"
	"github.com/major0/proton-utils/api"
	"github.com/major0/proton-utils/api/account"
	"github.com/major0/proton-utils/api/config"
	"github.com/major0/proton-utils/api/drive"
	"github.com/major0/proton-utils/internal/fusemount"
	fusedrv "github.com/major0/proton-utils/internal/fusemount/drive"
	"github.com/major0/proton-utils/internal/keyring"
	"github.com/major0/proton-utils/internal/sdnotify"
	"github.com/spf13/pflag"
)

// daemonConfig holds the resolved configuration from CLI flags.
type daemonConfig struct {
	account     string
	logLevel    slog.Level
	configPath  string
	sessionFile string
	mountpoint  string
}

// parseFlags parses CLI flags from args and returns a resolved daemonConfig.
// It uses pflag.FlagSet for testability and POSIX-style flag compaction.
func parseFlags(args []string) (daemonConfig, error) {
	fs := pflag.NewFlagSet("proton-fuse", pflag.ContinueOnError)

	var (
		account     string
		logLevel    string
		verbose     int
		configPath  string
		sessionFile string
		mountpoint  string
	)

	fs.StringVar(&account, "account", "", "select which account to use")
	fs.StringVar(&logLevel, "log-level", "", "log level: debug, info, warn, error")
	fs.CountVarP(&verbose, "verbose", "v", "increase verbosity (repeatable: -v = info, -vv = debug)")
	fs.StringVar(&configPath, "config", "", "override config file path")
	fs.StringVar(&sessionFile, "session-file", "", "override session index file path")
	fs.StringVar(&mountpoint, "mountpoint", "", "override mount path")

	if err := fs.Parse(args); err != nil {
		return daemonConfig{}, err
	}

	// Resolve log level: --log-level takes priority over -v count.
	resolvedLevel := resolveLogLevel(logLevel, verbose)

	// Resolve config path default.
	if configPath == "" {
		configPath = keyring.XDGConfigPath("config.yaml")
	}

	// Resolve session file default.
	if sessionFile == "" {
		sessionFile = keyring.XDGConfigPath("sessions.db")
	}

	// Resolve mountpoint default.
	if mountpoint == "" {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			return daemonConfig{}, errors.New("XDG_RUNTIME_DIR is not set and --mountpoint was not provided")
		}
		mountpoint = filepath.Join(runtimeDir, "proton", "fs")
	}

	return daemonConfig{
		account:     account,
		logLevel:    resolvedLevel,
		configPath:  configPath,
		sessionFile: sessionFile,
		mountpoint:  mountpoint,
	}, nil
}

// resolveLogLevel determines the effective log level. If logLevel is explicitly
// set, it takes priority. Otherwise, the verbose count is used.
func resolveLogLevel(logLevel string, verbose int) slog.Level {
	if logLevel != "" {
		switch strings.ToLower(logLevel) {
		case "debug":
			return slog.LevelDebug
		case "info":
			return slog.LevelInfo
		case "warn":
			return slog.LevelWarn
		case "error":
			return slog.LevelError
		default:
			// Invalid level falls through to verbose-based resolution.
		}
	}

	switch {
	case verbose >= 2:
		return slog.LevelDebug
	case verbose == 1:
		return slog.LevelInfo
	default:
		return slog.LevelWarn
	}
}

// xdgStatePath returns a path under $XDG_STATE_HOME/protonfs/.
// If XDG_STATE_HOME is unset, it defaults to ~/.local/state/protonfs/.
func xdgStatePath(name string) string {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, _ := os.UserHomeDir()
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "protonfs", name)
}

// configureLogging sets up the default slog logger based on the resolved level.
// It always writes structured JSON logs to stderr at the configured level.
// When level is debug, it additionally opens persistent debug log files under
// $XDG_STATE_HOME/protonfs/logs/ (fuse.log and drive.log).
// The returned cleanup function closes any opened file handles and should be
// deferred by the caller.
func configureLogging(level slog.Level) (cleanup func(), err error) {
	// Non-debug: single JSON handler writing to stderr.
	if level != slog.LevelDebug {
		handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})
		slog.SetDefault(slog.New(handler))
		return func() {}, nil
	}

	// Debug: create log directory and open debug files.
	logDir := xdgStatePath("logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}

	fuseLogPath := filepath.Join(logDir, "fuse.log")
	fuseLog, err := os.OpenFile(filepath.Clean(fuseLogPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening fuse.log: %w", err)
	}

	driveLogPath := filepath.Join(logDir, "drive.log")
	driveLog, err := os.OpenFile(filepath.Clean(driveLogPath), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		_ = fuseLog.Close()
		return nil, fmt.Errorf("opening drive.log: %w", err)
	}

	// Combine stderr and both debug files into a single writer for the
	// default logger. Per-service loggers (fuse-specific, drive-specific)
	// can be added later by creating child loggers with distinct writers.
	debugWriter := io.MultiWriter(os.Stderr, fuseLog, driveLog)
	handler := slog.NewJSONHandler(debugWriter, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	cleanup = func() {
		_ = fuseLog.Close()
		_ = driveLog.Close()
	}
	return cleanup, nil
}

// userAgent identifies the proton-fuse process in API requests.
const userAgent = "proton-fuse/v0.1.0"

// responseHeaderTimeout is the maximum time to wait for response headers
// from the Proton API. This catches dead connections without affecting
// body transfers (uploads/downloads can take as long as needed).
const responseHeaderTimeout = 30 * time.Second

// fuseCacheTimeout controls how long the kernel caches directory entries
// and file attributes before re-validating with the FUSE daemon. A short
// timeout balances reducing redundant Getattr/Lookup calls against
// ensuring freshness for a network-backed filesystem.
const fuseCacheTimeout = 1 * time.Second

// refreshInterval is the period between share refresh and proactive token
// refresh checks. Both tasks run on the same ticker to simplify shutdown.
const refreshInterval = 5 * time.Minute

// requestTimeoutHook sets ResponseHeaderTimeout on the default transport
// so individual API calls time out on dead connections. Body transfers
// (uploads/downloads) are unaffected.
func requestTimeoutHook(_ *proton.Manager) {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = responseHeaderTimeout
		}
	}
}

func main() {
	cfg, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Configure logging before any other operations.
	logCleanup, err := configureLogging(cfg.logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: configuring logging: %v\n", err)
		os.Exit(1)
	}

	exitCode := 0
	defer func() {
		logCleanup()
		if exitCode != 0 {
			os.Exit(exitCode)
		}
	}()

	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitCode = 1
	}
}

// run executes the daemon startup sequence. Returning an error causes main to
// exit non-zero after running deferred cleanup (including log file handles).
func run(cfg daemonConfig) error {
	// Step 1: Load config.
	appCfg, err := config.LoadConfig(cfg.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Step 2: Build SessionConfig from loaded config.
	sessionCfg := config.BuildSessionConfig(appCfg, appCfg.MaxJobs.Value())

	// Resolve account name: --account flag → config default for "protonfs".
	acctName := cfg.account
	if acctName == "" {
		acctName = appCfg.DefaultAccount("protonfs")
	}

	// Build Proton options for session restore.
	svc, err := api.LookupService("drive")
	if err != nil {
		return fmt.Errorf("looking up drive service: %w", err)
	}

	opts := []proton.Option{
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(svc.AppVersion("")),
		proton.WithUserAgent(userAgent),
	}
	if cfg.logLevel == slog.LevelDebug {
		opts = append(opts, proton.WithDebug(true))
	}

	// Step 3: Construct SessionIndex stores.
	kr := keyring.SystemKeyring{}
	store := keyring.NewSessionStore(cfg.sessionFile, acctName, "drive", kr)
	accountStore := keyring.NewSessionStore(cfg.sessionFile, acctName, "account", kr)
	cookieStore := keyring.NewSessionStore(cfg.sessionFile, acctName, "cookie", kr)

	// Step 4: Restore session.
	ctx := context.Background()
	session, err := account.RestoreServiceSession(
		ctx, "drive", opts,
		store, accountStore, cookieStore,
		svc.AppVersion(""), requestTimeoutHook,
	)
	if err != nil {
		if errors.Is(err, api.ErrNotLoggedIn) {
			return fmt.Errorf("not logged in (account %q). Run 'proton login' first", acctName)
		}
		return fmt.Errorf("restoring session: %w", err)
	}

	// Step 5: Register auth/deauth handlers.
	session.AddAuthHandler(account.NewAuthHandler(store, session))
	session.AddDeauthHandler(account.NewDeauthHandler())

	// Step 6: Set Session.Config and UserAgent.
	session.Config = sessionCfg
	session.UserAgent = userAgent

	// Step 7: Compute prefetch configuration from config.
	prefetchBlocks := appCfg.PrefetchBlocks.Value()
	if prefetchBlocks < 0 {
		prefetchBlocks = 0
	}
	if prefetchBlocks > 64 {
		prefetchBlocks = 64
	}

	// Step 8: Construct drive client.
	driveClient, err := drive.NewClient(ctx, session)
	if err != nil {
		return fmt.Errorf("creating drive client: %w", err)
	}
	driveClient.Config = sessionCfg
	driveClient.InitObjectCache()
	driveClient.PrefetchBlocks = prefetchBlocks

	// Step 8b: Validate and propagate block cache mode.
	blockCacheMode := appCfg.BlockCacheMode.Value()
	if blockCacheMode != "encrypted" && blockCacheMode != "decrypted" {
		return fmt.Errorf("invalid block_cache_mode %q (must be \"encrypted\" or \"decrypted\")", blockCacheMode)
	}
	slog.Info("block cache mode", "mode", blockCacheMode)
	driveClient.BlockCacheMode = blockCacheMode

	// Step 9: Construct DriveHandler and load shares.
	handler := fusedrv.NewDriveHandler(driveClient)
	if err := handler.LoadShares(ctx); err != nil {
		return fmt.Errorf("loading shares: %w", err)
	}

	// Step 10: Register in NamespaceRegistry.
	registry := fusemount.NewRegistry()
	registry.Register("drive", handler)

	// Step 11: Mount FUSE filesystem.
	mountCfg := fusemount.MountConfig{
		Mountpoint:     cfg.mountpoint,
		EntryTimeout:   fuseCacheTimeout,
		AttrTimeout:    fuseCacheTimeout,
		PrefetchBlocks: prefetchBlocks,
	}
	server, err := fusemount.Mount(mountCfg, registry)
	if err != nil {
		return fmt.Errorf("mounting filesystem: %w", err)
	}

	// Step 12: Signal systemd readiness.
	if err := sdnotify.Ready(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: sd_notify: %v\n", err)
	}

	slog.Info("proton-fuse ready", "mountpoint", cfg.mountpoint)

	// Step 13: Start combined refresh goroutine.
	// Initialize lastRefresh from persisted credentials.
	creds, err := store.Load()
	var lastRefresh time.Time
	if err == nil && creds != nil {
		lastRefresh = creds.LastRefresh
	}

	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	refreshDone := make(chan struct{})
	go func() {
		defer close(refreshDone)
		startRefreshLoop(refreshCtx, handler, session, lastRefresh)
	}()

	// Step 14: Signal wait — SIGTERM/SIGINT triggers graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	slog.Info("shutdown signal received, stopping")

	// Cancel refresh goroutine first.
	refreshCancel()
	<-refreshDone

	// Unmount and wait for in-flight FUSE operations.
	if err := server.Unmount(); err != nil {
		return fmt.Errorf("unmount: %w", err)
	}
	server.Wait()
	return nil
}

// startRefreshLoop runs the combined share refresh and proactive token
// refresh on a periodic ticker. It blocks until ctx is cancelled.
func startRefreshLoop(ctx context.Context, handler *fusedrv.DriveHandler, session *api.Session, lastRefresh time.Time) {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Share refresh.
			if err := handler.RefreshShares(ctx); err != nil {
				slog.Warn("share refresh failed", "error", err)
			}

			// Proactive token refresh — trigger a lightweight API call
			// if the session's token age exceeds the threshold.
			if account.NeedsProactiveRefresh(lastRefresh) {
				if _, err := session.Client.GetUser(ctx); err != nil {
					slog.Warn("proactive token refresh failed", "error", err)
				} else {
					slog.Debug("proactive token refresh succeeded")
					lastRefresh = time.Now()
				}
			}
		}
	}
}
