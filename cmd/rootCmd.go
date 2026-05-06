package cli

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	common "github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/account"
	"github.com/major0/proton-cli/api/config"
	"github.com/major0/proton-cli/api/drive"
	"github.com/spf13/cobra"
)

// rootParamsType holds the parsed root command flags.
type rootParamsType struct {
	Account            string
	ConfigFile         string
	LogLevel           string
	MaxWorkers         int
	SessionFile        string
	Verbose            int
	Timeout            time.Duration
	AppVersionOverride string
}

var (
	logLevel = new(slog.LevelVar)

	// rootCmd parameter store. Only the results of Flags and our preRun
	// flag cleanups should be stored here.
	rootParams rootParamsType

	rootCmd = &cobra.Command{
		Use:              "proton [options] <command>",
		Short:            "proton is a command line interface for Proton services",
		Long:             `proton is a command line interface for managing Proton services (Drive, Mail, etc.)`,
		TraverseChildren: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			// Start profiling if --profile was set (no-op without build tag).
			stopProfile := StartProfile()
			cobra.OnFinalize(stopProfile)

			// --log-level takes priority over -v count.
			switch strings.ToLower(rootParams.LogLevel) {
			case "trace", "debug":
				logLevel.Set(slog.LevelDebug)
			case "info":
				logLevel.Set(slog.LevelInfo)
			case "warn", "warning":
				logLevel.Set(slog.LevelWarn)
			case "error":
				logLevel.Set(slog.LevelError)
			case "":
				// Fall back to -v count.
				switch {
				case rootParams.Verbose == 1:
					logLevel.Set(slog.LevelInfo)
				case rootParams.Verbose > 1:
					logLevel.Set(slog.LevelDebug)
				default:
					logLevel.Set(slog.LevelWarn)
				}
			default:
				return fmt.Errorf("invalid --log-level %q (use: debug, info, warn, error)", rootParams.LogLevel)
			}

			if logLevel.Level() <= slog.LevelDebug {
				slog.Debug("verbosity", "log_level", logLevel.Level(), "verbose", rootParams.Verbose)
			}

			if rootParams.ConfigFile == "" {
				rootParams.ConfigFile = xdgConfigPath("config.yaml")
			}

			if rootParams.SessionFile == "" {
				rootParams.SessionFile = xdgConfigPath("sessions.db")
			}

			debugHTTP := rootParams.Verbose >= 3 || strings.EqualFold(rootParams.LogLevel, "debug") || strings.EqualFold(rootParams.LogLevel, "trace")

			opts := []proton.Option{
				proton.WithHostURL(APIHost),
				proton.WithAppVersion(AppVersion),
				proton.WithUserAgent(UserAgent),
			}
			if debugHTTP {
				opts = append(opts, proton.WithDebug(true))
			}

			// Load application config.
			cfg, err := config.LoadConfig(rootParams.ConfigFile)
			if err != nil {
				slog.Warn("config load failed, using defaults", "error", err)
				cfg = config.DefaultConfig()
			}

			rc := &RuntimeContext{
				Timeout:            rootParams.Timeout,
				DebugHTTP:          debugHTTP,
				ProtonOpts:         opts,
				SessionStore:       NewSessionStore(rootParams.SessionFile, rootParams.Account, "*", SystemKeyring{}),
				AccountStore:       NewSessionStore(rootParams.SessionFile, rootParams.Account, "account", SystemKeyring{}),
				CookieStore:        NewSessionStore(rootParams.SessionFile, rootParams.Account, "cookie", SystemKeyring{}),
				Account:            rootParams.Account,
				ServiceName:        "*",
				AppVersionOverride: rootParams.AppVersionOverride,
				Config:             cfg,
				SessionFile:        rootParams.SessionFile,
				Verbose:            rootParams.Verbose,
			}

			// Apply CLI flag overrides to Config Params.
			if f := cmd.Flags().Lookup("max-jobs"); f != nil && f.Changed {
				cfg.MaxJobs.SetCLI(rootParams.MaxWorkers)
			}

			SetContext(cmd, rc)

			return nil
		},
		Run: func(cmd *cobra.Command, _ []string) {
			_ = cmd.Help()
		},
	}
)

// AddCommand registers a subcommand with the root command.
func AddCommand(cmd *cobra.Command) {
	rootCmd.AddCommand(cmd)
}

// SetServiceCmd configures the service context on a command. Subcommands
// call this to set the service name, session store, and Proton options
// on RuntimeContext.
func SetServiceCmd(cmd *cobra.Command, service string) {
	rc := GetContext(cmd)
	rc.ServiceName = service
	svc, _ := common.LookupService(service)

	rc.SessionStore = NewSessionStore(
		rc.SessionFile, rc.Account, service, SystemKeyring{},
	)

	rc.ProtonOpts = []proton.Option{
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(AppVersion),
		proton.WithUserAgent(UserAgent),
	}

	if rc.DebugHTTP {
		rc.ProtonOpts = append(rc.ProtonOpts, proton.WithDebug(true))
	}
}

// resolveVersionRC returns the app version override for a service using
// RuntimeContext values. Checks (in order): AppVersionOverride flag,
// subsystem config, core config. Returns empty string when no override is
// configured, allowing the service's default version to be used.
func resolveVersionRC(rc *RuntimeContext, service string) string {
	if rc.AppVersionOverride != "" {
		return rc.AppVersionOverride
	}
	if rc.Config != nil {
		if sub, ok := rc.Config.Subsystems[service]; ok && sub.AppVersion.IsSet() {
			return sub.AppVersion.Value()
		}
		if rc.Config.AppVersion.IsSet() {
			return rc.Config.AppVersion.Value()
		}
	}
	return ""
}

// resolveMaxJobs returns the effective max_jobs for the current invocation.
// Precedence: CLI flag → subsystem override → core config → DefaultMaxWorkers().
func resolveMaxJobs(cmd *cobra.Command, rc *RuntimeContext) int {
	// CLI flag takes highest precedence.
	if f := cmd.Root().PersistentFlags().Lookup("max-jobs"); f != nil && f.Changed {
		return rootParams.MaxWorkers
	}
	if rc.Config != nil {
		// Subsystem override for the active service.
		if rc.ServiceName != "" && rc.ServiceName != "*" {
			if sub, ok := rc.Config.Subsystems[rc.ServiceName]; ok && sub.MaxJobs.IsSet() {
				return sub.MaxJobs.Value()
			}
		}
		// Core config.
		if rc.Config.MaxJobs.IsSet() {
			return rc.Config.MaxJobs.Value()
		}
	}
	return common.DefaultMaxWorkers()
}

// SetupSession returns a fully initialized, ready-to-use session by
// reading all per-invocation state from RuntimeContext. It calls
// api/account/ restore primitives, sets BaseURL/AppVersion/UserAgent,
// loads config onto Session.Config, and returns a ready session.
//
// When RuntimeContext.ServiceName is set to a specific service (not "*"),
// it uses RestoreServiceSession which handles auto-forking from the
// account session.
func SetupSession(ctx context.Context, cmd *cobra.Command) (*common.Session, error) {
	rc := GetContext(cmd)

	if rc.ServiceName != "" && rc.ServiceName != "*" {
		svc, _ := common.LookupService(rc.ServiceName)
		version := resolveVersionRC(rc, rc.ServiceName)
		session, err := account.RestoreServiceSession(
			ctx, rc.ServiceName, rc.ProtonOpts,
			rc.SessionStore, rc.AccountStore, rc.CookieStore,
			svc.AppVersion(version), requestTimeoutHook,
		)
		if err != nil {
			return nil, err
		}
		session.UserAgent = UserAgent
		session.Config = sessionConfigFromRC(cmd, rc)
		return session, nil
	}

	session, err := account.ReadySession(ctx, rc.ProtonOpts, rc.SessionStore, rc.CookieStore, requestTimeoutHook)
	if err != nil {
		return nil, err
	}
	session.AppVersion = AppVersion
	session.UserAgent = UserAgent
	session.Config = sessionConfigFromRC(cmd, rc)
	return session, nil
}

// sessionConfigFromRC builds a SessionConfig from the RuntimeContext's
// loaded application config. The cmd is used for flag override detection.
// Returns nil when no config is available.
func sessionConfigFromRC(cmd *cobra.Command, rc *RuntimeContext) *common.SessionConfig {
	if rc.Config == nil {
		return nil
	}
	defaults := make(map[string]string)
	for name, sub := range rc.Config.Subsystems {
		if sub.Account.IsSet() {
			defaults[name] = sub.Account.Value()
		}
	}
	wm := rc.Config.MemoryCacheWatermark.Value()
	return &common.SessionConfig{
		Shares:                  rc.Config.Shares,
		Defaults:                defaults,
		MaxJobs:                 resolveMaxJobs(cmd, rc),
		MemoryCacheMinWatermark: wm[0],
		MemoryCacheMaxWatermark: wm[1],
	}
}

// requestTimeoutHook sets a per-request timeout on the proton.Manager's
// Resty client and adds a retry condition for timeouts. This ensures
// individual API calls time out even when the operation context is
// unbounded (context.Background), and automatically retry when a
// request times out (e.g., dead HTTP/2 connection).
func requestTimeoutHook(_ *proton.Manager) {
	// Set ResponseHeaderTimeout on the default transport. This times
	// out waiting for response headers (catches dead connections) but
	// does NOT affect body transfers — uploads and downloads can take
	// as long as needed. CookieTransport uses http.DefaultTransport
	// as its Base, so this applies to all API requests.
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		if t.ResponseHeaderTimeout == 0 {
			t.ResponseHeaderTimeout = rootParams.Timeout
		}
	}
}

// NewDriveClient creates a drive client from a session and applies config
// from RuntimeContext.
func NewDriveClient(ctx context.Context, session *common.Session) (*drive.Client, error) {
	dc, err := drive.NewClient(ctx, session)
	if err != nil {
		return nil, err
	}
	dc.Config = session.Config
	dc.InitObjectCache()
	return dc, nil
}

// Execute runs the root command and exits on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ConfigFilePath returns the resolved config file path.
func ConfigFilePath() string {
	return rootParams.ConfigFile
}

func init() {
	// Config is loaded in PersistentPreRunE via LoadConfig.
	logopts := &slog.HandlerOptions{
		Level: logLevel,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, logopts))
	slog.SetDefault(logger)
	// proton.WithLogger(common.Logger)

	rootCmd.PersistentFlags().CountVarP(&rootParams.Verbose, "verbose", "v", "Enable verbose output. Can be specified multiple times to increase verbosity.")
	rootCmd.PersistentFlags().StringVar(&rootParams.LogLevel, "log-level", "", "Set log level: debug, info, warn, error")
	rootCmd.PersistentFlags().StringVarP(&rootParams.Account, "account", "a", "default", "Nickname of the account to use. This can be any string the user desires.")
	rootCmd.PersistentFlags().StringVar(&rootParams.ConfigFile, "config-file", "", "Config file to use. Defaults to value XDG_CONFIG_FILE")
	rootCmd.PersistentFlags().StringVar(&rootParams.SessionFile, "session-file", "", "Session file to use. Defaults to value XDG_CACHE_FILE")
	rootCmd.PersistentFlags().DurationVarP(&rootParams.Timeout, "timeout", "t", 60*time.Second, "Timeout for requests.")
	rootCmd.PersistentFlags().IntVarP(&rootParams.MaxWorkers, "max-jobs", "j", 10, "Maximum number of jobs to run in parallel.")
	rootCmd.PersistentFlags().StringVar(&rootParams.AppVersionOverride, "app-version", "", "Override the app version string for this invocation")

	// Profile flag — only registers when built with -tags profile.
	RegisterProfileFlag()

	// Hide the help flags as it ends up sorted into everything, which is a bit confusing.
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.SilenceUsage = true
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Help for proton-cli")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true
}
