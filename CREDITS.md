# Credits

proton-cli builds on the work of several open-source projects and contributors.

## Core Dependencies

- [go-proton-api](https://github.com/ProtonMail/go-proton-api) — Official Proton API client library for Go. Provides SRP authentication, session management, Human Verification plumbing, and the Drive/Mail/Calendar API surface. proton-cli's `proton/` package wraps this library.

- [Proton-API-Bridge](https://github.com/henrybear327/Proton-API-Bridge) by Chun-Hung Tseng (henrybear327) — Third-party Proton API bridge focusing on Drive. henrybear327 also authored the [rclone protondrive backend](https://rclone.org/protondrive/), which was the first working third-party Drive client and the primary reference for how to interact with the Proton Drive API from Go. Much of the practical knowledge about session handling, key management, and API quirks comes from studying this work.

- [rclone](https://github.com/rclone/rclone) — The protondrive backend in rclone (authored by henrybear327) is the most battle-tested third-party Proton Drive integration. Its approach to authentication, token refresh, and error handling informed proton-cli's design.

## Prior Art

- [Keybase Filesystem (KBFS)](https://github.com/keybase/client/tree/master/go/kbfs) by Keybase/Zoom — Per-user encrypted FUSE filesystem with a system-wide symlink redirector. The proton-fuse architecture (per-user FUSE mount + setuid redirector at a well-known path) is directly modeled on KBFS's design. The [redirector](https://github.com/keybase/client/tree/master/go/kbfs/redirector) component demonstrates how to use UID-based symlink routing to provide per-user views from a single global mountpoint. BSD-3-Clause licensed.

- [protonmail-cli](https://github.com/dimkouv/protonmail-cli) by dimkouv — Early unofficial command-line utility for ProtonMail (Python). Demonstrated that a CLI client for Proton services was viable and desirable.

- [hydroxide](https://github.com/emersion/hydroxide) by Simon Ser (emersion) — Third-party ProtonMail CardDAV, IMAP, and SMTP bridge (Go). Pioneered the approach of translating standard protocols into Proton API calls. Its auth flow and session management were early references.

- [proton-bridge](https://github.com/ProtonMail/proton-bridge) — Official Proton Mail Bridge. Its `internal/hv` package is the canonical reference implementation for Human Verification handling in Go, including the browser-based CAPTCHA flow.

- [proton-python-client](https://github.com/ProtonMail/proton-python-client) — Official Python Proton client module. Useful reference for API authentication patterns.

## Contributors

- henrybear327 (Chun-Hung Tseng) — Former ProtonMail engineer. Author of Proton-API-Bridge and the rclone protondrive backend. His work made third-party Proton Drive access practical.
