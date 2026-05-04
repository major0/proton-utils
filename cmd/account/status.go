package accountCmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/major0/proton-cli/api"
	"github.com/major0/proton-cli/api/account"
	cli "github.com/major0/proton-cli/cmd"
	"github.com/spf13/cobra"
)

// sessionStatus describes the freshness of a session.
type sessionStatus string

const (
	statusFresh   sessionStatus = "fresh"
	statusWarn    sessionStatus = "warn"
	statusExpired sessionStatus = "expired"
	statusStale   sessionStatus = "stale"
	statusNone    sessionStatus = "none"
)

// serviceStatus holds display data for one service session.
type serviceStatus struct {
	Service     string        `json:"service"`
	Host        string        `json:"host"`
	ClientID    string        `json:"client_id,omitempty"`
	AppVersion  string        `json:"app_version,omitempty"`
	Status      sessionStatus `json:"status"`
	UID         string        `json:"uid,omitempty"`
	LastRefresh time.Time     `json:"last_refresh,omitempty"`
	Age         string        `json:"age,omitempty"`
	ExpiresIn   string        `json:"expires_in,omitempty"`
}

var statusJSON bool

var accountStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show session status for all services",
	Long:  "Display the state of all Proton service sessions (account, drive, lumo, etc.)",
	RunE:  runAccountStatus,
}

func init() {
	accountCmd.AddCommand(accountStatusCmd)
	accountStatusCmd.Flags().BoolVar(&statusJSON, "json", false, "Output as JSON")
}

// buildServiceStatus builds the status for a single service, given the
// account session's LastRefresh for staleness comparison.
func buildServiceStatus(svc api.ServiceConfig, cfg *api.SessionCredentials, acctRefresh time.Time, verbose bool) serviceStatus {
	ss := serviceStatus{
		Service: svc.Name,
		Host:    svc.Host,
	}

	if verbose {
		ss.ClientID = svc.ClientID
		ss.AppVersion = svc.AppVersion(api.DefaultVersion)
	}

	if cfg == nil {
		ss.Status = statusNone
		return ss
	}

	ss.UID = cfg.UID
	ss.LastRefresh = cfg.LastRefresh

	if cfg.LastRefresh.IsZero() {
		ss.Status = statusStale
		ss.Age = "unknown"
		ss.ExpiresIn = "unknown"
		return ss
	}

	age := time.Since(cfg.LastRefresh)
	ss.Age = age.Truncate(time.Second).String()

	remaining := api.TokenExpireAge - age
	switch {
	case remaining < 0:
		ss.ExpiresIn = "expired"
		ss.Status = statusExpired
	case age > api.TokenWarnAge:
		ss.ExpiresIn = remaining.Truncate(time.Second).String()
		ss.Status = statusWarn
	default:
		ss.ExpiresIn = remaining.Truncate(time.Second).String()
		ss.Status = statusFresh
	}

	// Override with staleness relative to account session for non-account services.
	if svc.Name != "account" && !acctRefresh.IsZero() && account.IsStale(acctRefresh, cfg.LastRefresh) {
		ss.Status = statusStale
	}

	return ss
}

func runAccountStatus(cmd *cobra.Command, _ []string) error {
	rc := cli.GetContext(cmd)
	sessionFile := cli.ConfigFilePath()
	if sessionFile != "" {
		sessionFile = sessionFile[:len(sessionFile)-len("config.yaml")] + "sessions.db"
	}

	kr := cli.SystemKeyring{}
	verbose := rc.DebugHTTP || false // verbose when -vv or higher

	// Check if any account exists.
	idx := cli.NewSessionStore(sessionFile, rc.Account, "*", kr)
	accounts, err := idx.List()
	if err != nil {
		return fmt.Errorf("reading session index: %w", err)
	}

	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "Not logged in.")
		os.Exit(1)
	}

	// Load account session for staleness comparison.
	acctStore := cli.NewSessionStore(sessionFile, rc.Account, "account", kr)
	acctCfg, _ := acctStore.Load()

	// Also try wildcard for backward compat.
	if acctCfg == nil {
		wildcardStore := cli.NewSessionStore(sessionFile, rc.Account, "*", kr)
		acctCfg, _ = wildcardStore.Load()
	}

	var acctRefresh time.Time
	if acctCfg != nil {
		acctRefresh = acctCfg.LastRefresh
	}

	// Build status for all registered services from the registry.
	serviceNames := make([]string, 0, len(api.Services))
	for name := range api.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	// Also check wildcard for backward compat.
	allServices := append([]string{"*"}, serviceNames...)

	var results []serviceStatus

	for _, svcName := range allServices {
		var svc api.ServiceConfig
		if svcName == "*" {
			svc = api.ServiceConfig{Name: "*", Host: "-", ClientID: "-"}
		} else {
			svc, _ = api.LookupService(svcName)
		}

		store := cli.NewSessionStore(sessionFile, rc.Account, svcName, kr)
		cfg, loadErr := store.Load()
		if loadErr != nil {
			cfg = nil
		}

		ss := buildServiceStatus(svc, cfg, acctRefresh, verbose)
		results = append(results, ss)
	}

	if statusJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}

	// Human-readable output.
	fmt.Fprintf(os.Stderr, "Account: %s\n\n", rc.Account)

	if verbose {
		fmt.Fprintf(os.Stderr, "%-12s  %-8s  %-14s  %-14s  %-40s  %-15s  %s\n",
			"SERVICE", "STATUS", "AGE", "EXPIRES IN", "HOST", "UID", "APP VERSION")
	} else {
		fmt.Fprintf(os.Stderr, "%-12s  %-8s  %-14s  %-14s  %s\n",
			"SERVICE", "STATUS", "AGE", "EXPIRES IN", "HOST")
	}

	for _, s := range results {
		uid := s.UID
		if uid == "" {
			uid = "-"
		} else if len(uid) > 12 {
			uid = uid[:12] + "..."
		}
		age := s.Age
		if age == "" {
			age = "-"
		}
		expires := s.ExpiresIn
		if expires == "" {
			expires = "-"
		}
		host := s.Host
		if len(host) > 40 {
			host = host[:40] + "..."
		}

		if verbose {
			appVer := s.AppVersion
			if appVer == "" {
				appVer = "-"
			}
			fmt.Fprintf(os.Stderr, "%-12s  %-8s  %-14s  %-14s  %-40s  %-15s  %s\n",
				s.Service, s.Status, age, expires, host, uid, appVer)
		} else {
			fmt.Fprintf(os.Stderr, "%-12s  %-8s  %-14s  %-14s  %s\n",
				s.Service, s.Status, age, expires, host)
		}
	}

	return nil
}
