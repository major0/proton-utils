package api

// SessionConfig holds resolved application configuration values.
// Subsystem clients read config from this struct — they never load
// config files directly. Consumers populate it via api/config/ and
// set it on the Session before passing the session to subsystems.
type SessionConfig struct {
	Shares   map[string]ShareConfig
	Defaults map[string]string

	// MaxJobs is the resolved max concurrent jobs (after precedence resolution).
	// Subsystems use this to size local semaphores or worker pools.
	MaxJobs int

	// MemoryCacheMinWatermark is the resolved minimum watermark in bytes.
	MemoryCacheMinWatermark int64

	// MemoryCacheMaxWatermark is the resolved maximum watermark in bytes.
	MemoryCacheMaxWatermark int64
}
