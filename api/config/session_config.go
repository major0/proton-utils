package config

import "github.com/major0/proton-utils/api"

// BuildSessionConfig converts a Config into a SessionConfig suitable for
// passing to subsystem clients. The maxJobs parameter allows callers to
// apply their own precedence resolution (CLI flag, subsystem override, etc.)
// before constructing the SessionConfig.
func BuildSessionConfig(cfg *Config, maxJobs int) *api.SessionConfig {
	defaults := make(map[string]string)
	for name, sub := range cfg.Subsystems {
		if sub.Account.IsSet() {
			defaults[name] = sub.Account.Value()
		}
	}
	wm := cfg.MemoryCacheWatermark.Value()
	return &api.SessionConfig{
		Shares:                  cfg.Shares,
		Defaults:                defaults,
		MaxJobs:                 maxJobs,
		MemoryCacheMinWatermark: wm[0],
		MemoryCacheMaxWatermark: wm[1],
	}
}
