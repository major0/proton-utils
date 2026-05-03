package api

// SessionConfig holds resolved application configuration values.
// Subsystem clients read config from this struct — they never load
// config files directly. Consumers populate it via api/config/ and
// set it on the Session before passing the session to subsystems.
type SessionConfig struct {
	Shares   map[string]ShareConfig
	Defaults map[string]string
}
