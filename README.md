# Proton CLI

A command-line interface for [Proton][] services.

> **Status:** Experimental — not production ready. Core operations work
> but the tool is under active development and APIs may change without
> notice.

> **⚠️ `api/` package:** The `api/` package is in extreme flux and is
> not suitable for external consumers. It will not stabilize until well
> after the `cmd/` and filesystem interfaces are stable, at which point
> it will undergo further optimization and refactoring.

## Features

- **[Proton Drive](docs/drive.md)** — full file management: ls, find,
  cp, mv, mkdir, rm, share management, volume usage
- **[Proton Lumo](docs/lumo.md)** — AI assistant: interactive chat,
  project spaces, and a local OpenAI-compatible API server
- **Parallelism** — bounded worker pools with shared rate-limit
  throttling; concurrent directory traversal and block I/O
- **Persistent Bearer sessions** — SRP authentication with automatic
  token refresh; credentials stored in the OS keyring
- **Persistent cookie sessions** — browser-based login for services
  that require cookie-scoped auth (Lumo); automatic cookie refresh
- **Multi-account support** — independent service sessions per account;
  switch with `--account`
- **Opt-in caching** — per-share dirent, metadata, and on-disk block
  caches; disabled by default, user-configurable
- **CAPTCHA handling** — automatic browser-based human verification
  via chromedp
- **Encrypted-first** — raw API objects are the canonical in-memory
  representation; decryption is lazy and on-demand; decrypted content
  is never persisted to disk

## Installation

Requires Go 1.22+ and Chrome/Chromium (for CAPTCHA during login).

```sh
git clone https://github.com/major0/proton-utils.git
cd proton-utils
make build
```

### Platform dependencies

| Platform | Packages |
|----------|----------|
| Ubuntu/Debian | `libsecret-1-dev` |
| Fedora/RHEL | `libsecret-devel` |
| macOS | None (uses Keychain) |

## Documentation

- [Account commands](docs/account.md) — login, logout, session management
- [Drive commands](docs/drive.md) — file operations, shares, volumes
- [Lumo commands](docs/lumo.md) — chat, spaces, OpenAI server

## License

See [LICENSE](LICENSE).

## Disclaimer

This project is not sponsored, endorsed, or affiliated with [Proton AG](https://proton.me).
Proton, Proton Drive, and Proton Mail are trademarks of Proton AG.

[Proton]: https://proton.me
