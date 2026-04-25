package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/ProtonMail/go-proton-api"
	common "github.com/major0/proton-cli/api"
	driveClient "github.com/major0/proton-cli/api/drive/client"
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
	Timeout time.Duration

	// DebugHTTP is true when verbosity >= 3, enabling HTTP debug logging.
	DebugHTTP bool

	// ProtonOpts holds the base Proton API options (app version, user agent).
	ProtonOpts []proton.Option

	// SessionStoreVar handles loading/saving session data.
	SessionStoreVar common.SessionStore

	// AccountStoreVar handles loading/saving the account session data.
	// Used by RestoreServiceSession as the fork source.
	AccountStoreVar common.SessionStore

	// CookieStoreVar handles loading/saving cookie-based session data.
	// Used by RestoreServiceSession for cookie auth persistence.
	CookieStoreVar common.SessionStore

	// Account holds the current --account flag value.
	Account string

	// ServiceName holds the current service context. Default is "*" (wildcard).
	// Subcommand PersistentPreRunE hooks call SetService to override this.
	ServiceName string

	// AppVersionOverride holds the --app-version flag value. When non-empty,
	// it overrides the version string for the current invocation.
	AppVersionOverride string

	// ConfigVar holds the loaded application config. Available to all subcommands.
	ConfigVar *common.Config

	// Private variables below this point

	logLevel = new(slog.LevelVar)

	// rootCmd parameter store. Only the results of Flags and our preRun
	// flag cleanups should be stored here.
	rootParams rootParamsType

	rootCmd = &cobra.Command{
		Use:   "proton [options] <command>",
		Short: "proton is a command line interface for Proton services",
		Long:  `proton is a command line interface for managing Proton services (Drive, Mail, etc.)`,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
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

			Timeout = rootParams.Timeout
			DebugHTTP = rootParams.Verbose >= 3 || strings.EqualFold(rootParams.LogLevel, "debug") || strings.EqualFold(rootParams.LogLevel, "trace")
			Account = rootParams.Account

			// Rebuild proton options based on verbosity.
			ProtonOpts = []proton.Option{
				proton.WithHostURL(APIHost),
				proton.WithAppVersion(AppVersion),
				proton.WithUserAgent(UserAgent),
			}

			if DebugHTTP {
				ProtonOpts = append(ProtonOpts, proton.WithDebug(true))
			}

			SessionStoreVar = internal.NewSessionStore(rootParams.SessionFile, rootParams.Account, "*", internal.SystemKeyring{})
			AccountStoreVar = internal.NewSessionStore(rootParams.SessionFile, rootParams.Account, "account", internal.SystemKeyring{})
			CookieStoreVar = internal.NewSessionStore(rootParams.SessionFile, rootParams.Account, "cookie", internal.SystemKeyring{})
			ServiceName = "*"

			// Load application config.
			cfg, err := common.LoadConfig(rootParams.ConfigFile)
			if err != nil {
				slog.Warn("config load failed, using defaults", "error", err)
				cfg = common.DefaultConfig()
			}
			ConfigVar = cfg

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

// SetService configures the CLI for a specific service. Called by subcommand
// group PersistentPreRunE hooks. It rebuilds SessionStoreVar with a
// service-specific store and rebuilds ProtonOpts with the service's host.
// The Resty client (used by go-proton-api for login, GetUser, etc.) uses
// the global AppVersion. DoJSON/DoSSE resolve per-host versions via
// resolveAppVersion.
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
		session, err := common.RestoreServiceSession(
			ctx, ServiceName, ProtonOpts,
			SessionStoreVar, AccountStoreVar, CookieStoreVar,
			svc.AppVersion(""), nil,
		)
		if err != nil {
			return nil, err
		}
		session.UserAgent = UserAgent
		return session, nil
	}

	session, err := common.ReadySession(ctx, ProtonOpts, SessionStoreVar, CookieStoreVar, nil)
	if err != nil {
		return nil, err
	}
	session.AppVersion = AppVersion
	session.UserAgent = UserAgent
	return session, nil
}

// NewDriveClient creates a drive client with the loaded config applied.
func NewDriveClient(ctx context.Context, session *common.Session) (*driveClient.Client, error) {
	dc, err := driveClient.NewClient(ctx, session)
	if err != nil {
		return nil, err
	}
	dc.Config = ConfigVar
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
	// cobra.OnInitialize(initConfig) // TODO
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
