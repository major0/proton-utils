# Configuration

The `proton config` command group manages application settings using
namespaced selectors.

## Commands

```sh
proton config list              # list all configuration keys
proton config get <key>         # get a configuration value
proton config set <key> <value> # set a configuration value
proton config unset <key>       # remove a configuration value
proton config show              # show the full configuration file
proton config resolve <key>     # show the resolved value with precedence
```

## Configuration File

Configuration is stored in YAML format at the XDG config path
(typically `~/.config/proton-utils/config.yaml`). Use `--config-file`
on the root command to override.

## Subsystem Overrides

Configuration supports per-subsystem overrides. For example, cache
settings can be configured per-share:

```sh
proton config set shares.<share-id>.memory_cache link_name
proton config set max_jobs 8
proton config set subsystems.drive.max_jobs 4
```

## Precedence

Configuration values are resolved with the following precedence
(highest to lowest):

1. CLI flags (`--max-jobs`, `--app-version`)
2. Subsystem config (`subsystems.<service>.<key>`)
3. Core config (`<key>`)
4. Built-in defaults
