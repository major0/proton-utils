package api

import (
	"errors"
	"fmt"
	"net/url"
)

// ErrUnknownService indicates that the requested service is not in the registry.
var ErrUnknownService = errors.New("unknown service")

// DefaultVersion is the fallback app version when no override is configured.
const DefaultVersion = "5.0.999.999"

// ServiceConfig holds per-service API configuration.
type ServiceConfig struct {
	Name       string // service name: "account", "drive", "lumo"
	Host       string // API base URL: "https://account.proton.me/api"
	ClientID   string // app identifier: "web-account", "web-drive", "web-lumo"
	Version    string // default version number for this service
	CookieAuth bool   // true if the service requires cookie-based auth for fork push
}

// AppVersion returns the x-pm-appversion header value for this service.
// Format: <clientID>@<version>
// This must match the format used by the official Proton web clients.
// The server validates this to determine scope grants on fork responses.
func (sc ServiceConfig) AppVersion(version string) string {
	if version == "" {
		version = sc.Version
	}
	return sc.ClientID + "@" + version
}

// Services is the global service registry.
var Services = map[string]ServiceConfig{
	"account": {Name: "account", Host: "https://account.proton.me/api", ClientID: "web-account", Version: "5.0.367.1", CookieAuth: true},
	"drive":   {Name: "drive", Host: "https://drive-api.proton.me/api", ClientID: "web-drive", Version: "5.2.0", CookieAuth: false},
	"lumo":    {Name: "lumo", Host: "https://lumo.proton.me/api", ClientID: "web-lumo", Version: "1.3.3.4", CookieAuth: true},
}

// hostIndex maps hostname → ServiceConfig for reverse lookup.
// Built once at init from the Services registry.
var hostIndex map[string]ServiceConfig

func init() {
	hostIndex = make(map[string]ServiceConfig, len(Services))
	for _, svc := range Services {
		u, err := url.Parse(svc.Host)
		if err != nil {
			continue
		}
		hostIndex[u.Hostname()] = svc
	}
}

// LookupService returns the ServiceConfig for the given name, or
// ErrUnknownService if the service is not registered.
func LookupService(name string) (ServiceConfig, error) {
	svc, ok := Services[name]
	if !ok {
		return ServiceConfig{}, fmt.Errorf("%w: %q", ErrUnknownService, name)
	}
	return svc, nil
}

// LookupServiceByHost returns the ServiceConfig whose Host URL matches the
// given hostname (e.g., "account.proton.me", "lumo.proton.me"). Returns
// ErrUnknownService if no service matches.
func LookupServiceByHost(host string) (ServiceConfig, error) {
	svc, ok := hostIndex[host]
	if !ok {
		return ServiceConfig{}, fmt.Errorf("%w: host %q", ErrUnknownService, host)
	}
	return svc, nil
}

// ResolveAppVersion determines the x-pm-appversion value for a request URL.
// If the URL targets a known service host, returns that service's app version.
// Otherwise returns the fallback value.
func ResolveAppVersion(reqURL, fallback string) string {
	u, err := url.Parse(reqURL)
	if err != nil || u.Host == "" {
		return fallback
	}
	svc, err := LookupServiceByHost(u.Hostname())
	if err != nil {
		return fallback
	}
	return svc.AppVersion("")
}

// AccountHost returns the account service's API base URL from the registry.
// Use this instead of hardcoding the account host URL.
func AccountHost() string {
	return Services["account"].Host
}
