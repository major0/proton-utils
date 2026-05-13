# Proton API References

Collected issues, forum threads, and resources that illuminate Proton API behavior for third-party clients. Organized by topic.

## Human Verification / CAPTCHA

- [rclone #7967 — proton-drive remote fails due to captcha](https://github.com/rclone/rclone/issues/7967) — CAPTCHA (9001) errors blocking rclone protondrive login. Demonstrates the problem proton-utils also hits.

- [rclone forum — Captcha error when using Proton Drive](https://forum.rclone.org/t/captcha-error-when-using-proton-drive/47879) — Community discussion of CAPTCHA triggers during rclone config. Notes that encryption keys must be pre-generated via browser login.

- [rclone forum — Rclone can't connect to Proton Drive (CAPTCHA errors)](https://forum.rclone.org/t/rclone-can-t-connect-to-proton-drive-captcha-errors/52302) — More recent CAPTCHA issues with rclone.

- [proton-bridge `internal/hv` package](https://pkg.go.dev/github.com/ProtonMail/proton-bridge/v3/internal/hv) — Official reference implementation for Human Verification in Go. Shows the browser-based CAPTCHA flow pattern.

- [ElectronMail #419 — Human Verification broken captcha workflow](https://github.com/vladimiry/ElectronMail/issues/419) — HV/CAPTCHA issues on non-standard API entry points.

- [gravilk/protonmail-documented — Proton CAPTCHA documented and solved](https://github.com/gravilk/protonmail-documented) — Documents the Proton CAPTCHA format and solving approach (July 2023, may be outdated).

- [gravilk — ProtonMail captcha solving function (gist)](https://gist.github.com/gravilk/1299aefa9324e33d1d84e0ad23b6f3ad) — Go implementation of a Proton CAPTCHA solver. Reference for understanding the CAPTCHA image format.

- [Proton blog — Introducing Proton CAPTCHA](https://www.proton.me/blog/proton-captcha) — Official announcement of Proton's own CAPTCHA system (replacing Google reCAPTCHA).

- [WebClients #242 — ProtonMail includes Google Recaptcha for Login](https://github.com/ProtonMail/WebClients/issues/242) — Historical context on Proton's transition from Google reCAPTCHA to their own CAPTCHA.

- [HN discussion — ProtonMail includes Google Recaptcha for login](https://news.ycombinator.com/item?id=27326243) — Community discussion about CAPTCHA privacy implications.

## Authentication / Session Management

- [rclone #7381 — Proton Drive session expires too quickly](https://github.com/rclone/rclone/issues/7381) — Session expiry behavior and token refresh patterns.

- [rclone forum — 2FA for proton drive keeps failing](https://forum.rclone.org/t/2fa-for-proton-drive-keeps-failing/52895) — 2FA issues with rclone, missing signature errors.

- [rclone forum — Protond Drive 2fa code doesn't work](https://forum.rclone.org/t/protond-drive-2fa-code-doesnt-work/47312) — TOTP timing issues during rclone config.

- [rclone forum — Some characters seem not to be handled in passwords](https://forum.rclone.org/t/some-characters-seem-not-to-be-handled-in-passwords/42760) — Password encoding edge cases with the Proton API.

- [hydroxide auth.go](https://github.com/emersion/hydroxide/blob/master/protonmail/auth.go) — hydroxide's authentication implementation, an early third-party Go reference.

## App Version String

- [ProtonMail/proton-version](https://github.com/ProtonMail/proton-version) — Official version string utilities. May contain format documentation.

- [go-proton-api response.go error codes](https://github.com/ProtonMail/go-proton-api) — Error codes 5001 (AppVersionMissing) and 5003 (AppVersionBad) define what happens with invalid version strings.

## Drive API

- [rclone #7266 — Error 422's & 400's](https://github.com/rclone/rclone/issues/7266) — Various API errors during Drive operations.

- [rclone forum — ProtonDrive: fix takes 100 lines](https://forum.rclone.org/t/protondrive-fix-takes-100-lines/53519) — Block upload API changes requiring per-block verification tokens.

- [rclone forum — ProtonDrive error: no keyring is generated](https://forum.rclone.org/t/protondrive-error-no-keyring-is-generated/47611) — Keyring generation requirements for Drive access.

- [rclone #8870 — Can't sync with Proton Drive](https://github.com/rclone/rclone/issues/8870) — Recent sync failures, may indicate API changes.

- [rclone forum — How feasible is Proton Drive support?](https://forum.rclone.org/t/how-feasible-is-proton-drive-support/39860) — Original discussion about adding Proton Drive to rclone, with technical feasibility analysis.

- [Proton-API-Bridge #1 — What about a README to use this project?](https://github.com/henrybear327/Proton-API-Bridge/issues/1) — Early usage documentation for the API bridge.

## Third-Party Clients

- [henrybear327/Proton-API-Bridge](https://github.com/henrybear327/Proton-API-Bridge) — Go, Drive-focused. The most active third-party Go client.

- [dimkouv/protonmail-cli](https://github.com/dimkouv/protonmail-cli) — Python, Mail-focused. Early unofficial CLI.

- [emersion/hydroxide](https://github.com/emersion/hydroxide) — Go, Mail-focused (IMAP/SMTP/CardDAV bridge).

- [trevorhobenshield/proton-api-client](https://github.com/trevorhobenshield/proton-api-client) — Python, Mail API client.

- [opulentfox-29/protonmail-api-client](https://github.com/opulentfox-29/protonmail-api-client) — Python, Mail API client with template rendering.

- [AzureFlow/proton-poc](https://github.com/AzureFlow/proton-poc) — Proof of concept Proton CAPTCHA solver.

- [proton-api-rs](https://www.lib.rs/crates/proton-api-rs) — Rust Proton API client (minimal maintenance).

## Official Proton Repositories

- [ProtonMail/go-proton-api](https://github.com/ProtonMail/go-proton-api) — Go API client library.
- [ProtonMail/proton-bridge](https://github.com/ProtonMail/proton-bridge) — Official Mail Bridge (Go). Reference for HV, auth, session handling.
- [ProtonMail/proton-python-client](https://github.com/ProtonMail/proton-python-client) — Official Python client.
- [ProtonMail/WebClients](https://github.com/ProtonMail/WebClients) — Web client monorepo. Useful for understanding API endpoints.
- [ProtonMail/gopenpgp](https://github.com/ProtonMail/gopenpgp) — OpenPGP library used for encryption.
- [ProtonDriveApps/android-drive](https://github.com/ProtonDriveApps/android-drive) — Official Android Drive client (open source).
- [protonpass/pass-cli](https://github.com/protonpass/pass-cli) — Official Pass CLI (docs only, binary is closed source).

## rclone Proton Drive Documentation

- [rclone.org/protondrive](https://rclone.org/protondrive/) — Official rclone documentation for the Proton Drive backend. Covers configuration, authentication, and known limitations.
