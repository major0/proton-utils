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
	"github.com/major0/proton-cli/internal"
	"github.com/spf13/cobra"
)

// rootParamsType holds the parsed root command flags.
type rootParamsType struct {
	Account     string
	ConfigFile  string
	LogLevel    string
	MaxWorkers  int
	SessionFile string
	Verbose     int
	Timeout     time.Duration
}

var (
	// Timeout holds the global request timeout duration.
	// Migration: subcommands should use GetContext(cmd).Timeout instead.
	Timeout time.Duration

	// DebugHTTP is true when verbosity >= 3, enabling HTTP debug logging.
	// Migration: subcommands should use GetContext(cmd).DebugHTTP instead.
	DebugHTTP bool

	// ProtonOpts holds the base Proton API options (app version, user agent).
	// Migration: subcommands should use GetContext(cmd).ProtonOpts instead.
	ProtonOpts []proton.Option

	// SessionStoreVar handles loading/saving session data.
	// Migration: subcommands should use GetContext(cmd).SessionStore instead.
	SessionStoreVar common.SessionStore

	// AccountStoreVar handles loading/saving the account session data.
	// Migration: subcommands should use GetContext(cmd).AccountStore instead.
	AccountStoreVar common.SessionStore

	// CookieStoreVar handles loading/saving cookie-based session data.
	// Migration: subcommands should use GetContext(cmd).CookieStore instead.
	CookieStoreVar common.SessionStore

	// Account holds the current --account flag value.
	// Migration: subcommands should use GetContext(cmd).Account instead.
	Account string

	// ServiceName holds the current service context. Default is "*" (wildcard).
	// Migration: subcommands should use GetContext(cmd).ServiceName instead.
	ServiceName string

	// AppVersionOverride holds the --app-version flag value.
	// Migration: subcommands should use GetContext(cmd).AppVersionOverride instead.
	AppVersionOverride string

	// ConfigVar holds the loaded application config.
	// Migration: subcommands should use GetContext(cmd).Config instead.
	ConfigVar *config.Config

	// Private variables below this point

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
				SessionStore:       internal.NewSessionStore(rootParams.SessionFile, rootParams.Account, "*", internal.SystemKeyring{}),
				AccountStore:       internal.NewSessionStore(rootParams.SessionFile, rootParams.Account, "account", internal.SystemKeyring{}),
				CookieStore:        internal.NewSessionStore(rootParams.SessionFile, rootParams.Account, "cookie", internal.SystemKeyring{}),
				Account:            rootParams.Account,
				ServiceName:        "*",
				AppVersionOverride: AppVersionOverride,
				Config:             cfg,
				SessionFile:        rootParams.SessionFile,
				Verbose:            rootParams.Verbose,
			}
			SetContext(cmd, rc)

			// Sync deprecated globals from RuntimeContext.
			Timeout = rc.Timeout
			DebugHTTP = rc.DebugHTTP
			ProtonOpts = rc.ProtonOpts
			SessionStoreVar = rc.SessionStore
			AccountStoreVar = rc.AccountStore
			CookieStoreVar = rc.CookieStore
			Account = rc.Account
			ServiceName = rc.ServiceName
			ConfigVar = rc.Config

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

// SetService configures the CLI for a specific service.
// Migration: use SetServiceCmd instead.
func SetService(service string) {
	ServiceName = service
	svc, _ := common.LookupService(service)

	SessionStoreVar = internal.NewSessionStore(
		rootParams.SessionFile, Account, service, internal.SystemKeyring{},
	)

	ProtonOpts = []proton.Option{
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(AppVersion),
		proton.WithUserAgent(UserAgent),
	}

	if DebugHTTP {
		ProtonOpts = append(ProtonOpts, proton.WithDebug(true))
	}
}

// SetServiceCmd is the context-aware version of SetService. Subcommands
// should migrate to this once they use GetContext.
func SetServiceCmd(cmd *cobra.Command, service string) {
	rc := GetContext(cmd)
	rc.ServiceName = service
	svc, _ := common.LookupService(service)

	rc.SessionStore = internal.NewSessionStore(
		rc.SessionFile, rc.Account, service, internal.SystemKeyring{},
	)

	rc.ProtonOpts = []proton.Option{
		proton.WithHostURL(svc.Host),
		proton.WithAppVersion(AppVersion),
		proton.WithUserAgent(UserAgent),
	}

	if rc.DebugHTTP {
		rc.ProtonOpts = append(rc.ProtonOpts, proton.WithDebug(true))
	}

	// Sync deprecated globals.
	SetService(service)
}

// resolveVersion returns the app version string for a service, checking
// (in order): --app-version flag, config file override, DefaultVersion.
func resolveVersion(service string) string {
	if AppVersionOverride != "" {
		return AppVersionOverride
	}
	if ConfigVar != nil {
		if v := ConfigVar.ServiceVersion(service, ""); v != "" {
			return v
		}
	}
	return common.DefaultVersion
}

// RestoreSession returns a fully initialized, ready-to-use session using
// the package-level ProtonOpts and SessionStoreVar. When ServiceName is set
// to a specific service (not "*"), it uses RestoreServiceSession which
// handles auto-forking from the account session.
func RestoreSession(ctx context.Context) (*common.Session, error) {
	if ServiceName != "" && ServiceName != "*" {
		svc, _ := common.LookupService(ServiceName)
		session, err := account.RestoreServiceSession(
			ctx, ServiceName, ProtonOpts,
			SessionStoreVar, AccountStoreVar, CookieStoreVar,
			svc.AppVersion(""), requestTimeoutHook,
		)
		if err != nil {
			return nil, err
		}
		session.UserAgent = UserAgent
		return session, nil
	}

	session, err := account.ReadySession(ctx, ProtonOpts, SessionStoreVar, CookieStoreVar, requestTimeoutHook)
	if err != nil {
		return nil, err
	}
	session.AppVersion = AppVersion
	session.UserAgent = UserAgent
	return session, nil
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
			t.ResponseHeaderTimeout = Timeout
		}
	}
}

// NewDriveClient creates a drive client with the loaded config applied.
func NewDriveClient(ctx context.Context, session *common.Session) (*drive.Client, error) {
	dc, err := drive.NewClient(ctx, session)
	if err != nil {
		return nil, err
	}
	dc.Config = ConfigVar
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
	rootCmd.PersistentFlags().StringVar(&AppVersionOverride, "app-version", "", "Override the app version string for this invocation")

	// Profile flag — only registers when built with -tags profile.
	RegisterProfileFlag()

	// Hide the help flags as it ends up sorted into everything, which is a bit confusing.
	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.SilenceUsage = true
	rootCmd.PersistentFlags().BoolP("help", "h", false, "Help for proton-cli")
	rootCmd.PersistentFlags().Lookup("help").Hidden = true
}
