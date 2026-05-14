# ProtonFS — FUSE Filesystem

ProtonFS provides a per-user FUSE filesystem that exposes Proton Drive
as a local directory tree. It consists of two components:

- **proton-fuse** — per-user FUSE daemon that mounts Proton Drive
- **proton-redirector** — system-wide setuid redirector at `/proton`

## Architecture

The design is modeled on [Keybase KBFS](https://github.com/keybase/client/tree/master/go/kbfs):
a global mountpoint (`/proton`) uses per-UID symlinks to route each user
to their own private FUSE mount.

```
/proton/                          ← proton-redirector (setuid, system-wide)
  drive → $XDG_RUNTIME_DIR/proton/fs/drive   ← per-user symlink

$XDG_RUNTIME_DIR/proton/fs/       ← proton-fuse (per-user)
  drive/
    My files/
      Documents/
      Photos/
    ...
```

Each user runs their own `proton-fuse` instance. The redirector resolves
the calling process's UID and returns a symlink to that user's mount.

## Platform

Linux only. Requires FUSE support (`/dev/fuse`) and `fusermount`.

## Prerequisites

1. A logged-in Proton account: `proton account login -u <username>`
2. `fusermount` available in PATH
3. `/proton` directory pre-created by `make install` (mode 0555, root-owned)

## Installation

```sh
make build
sudo make install-protonfs
```

This installs:
- `proton-fuse` → `/usr/local/sbin/proton-fuse`
- `proton-redirector` → `/usr/local/sbin/proton-redirector` (setuid)
- `protonctl` → `/usr/local/bin/protonctl`
- `/proton/` directory (mode 0555)
- systemd user units: `protonfs.service`, `protonfs-redirector.service`

## Usage

### protonctl

The `protonctl` script manages the systemd user services:

```sh
protonctl enable    # enable and start protonfs
protonctl disable   # stop and disable protonfs
protonctl start     # start protonfs services
protonctl stop      # stop protonfs services
protonctl reload    # reload unit files and restart
protonctl status    # show service status
```

### Manual start

```sh
# Start the redirector (requires setuid or root)
proton-redirector /proton

# Start the per-user FUSE mount
proton-fuse
```

### proton-fuse flags

```
--account <name>     Select which account to use
--mountpoint <path>  Override mount path (default: $XDG_RUNTIME_DIR/proton/fs)
--config <path>      Override config file path
--session-file <path> Override session index file path
--log-level <level>  Log level: debug, info, warn, error
-v                   Increase verbosity (repeatable)
```

## Filesystem Layout

The mount root contains namespace directories. Currently only `drive/`
is registered:

```
$XDG_RUNTIME_DIR/proton/fs/
└── drive/
    ├── My files/
    │   ├── Documents/
    │   └── ...
    └── Photos/
```

Each share appears as a top-level directory under `drive/`. Files and
directories mirror the Proton Drive structure with lazy decryption —
names are decrypted on readdir, content on read.

## Systemd Integration

Both services use `Type=notify` and signal readiness via `sd_notify`.
The FUSE unit depends on the redirector:

```
protonfs-redirector.service → protonfs.service
```

Enable both with `protonctl enable` or individually:

```sh
systemctl --user enable --now protonfs-redirector.service
systemctl --user enable --now protonfs.service
```

## Session Management

`proton-fuse` restores the Drive session from the same session store
used by the CLI (`proton account login`). It runs a background refresh
loop to keep tokens fresh during long-running operation.

The `--account` flag selects which stored account to use. If not
specified, it uses the configured default for the `protonfs` subsystem
(set via `proton config set subsystems.protonfs.account <name>`).

## Security

- The redirector clears its environment on startup (except `NOTIFY_SOCKET`)
- The redirector validates mountpoint ownership (root) and permissions (no group/other write)
- Per-user mounts are at `$XDG_RUNTIME_DIR` (RAM-backed, per-session)
- Mount directories are created with mode 0700
- Stale FUSE mounts are detected and cleaned automatically on startup
