# Account Commands

The `proton account` command group manages authentication and session
lifecycle.

## Login

```sh
proton account login [options]
```

Options:
- `-u <username>` — Proton username or email
- `--cookie-session` — use cookie-based authentication (required for Lumo)

### Authentication modes

**Bearer (default):** Traditional SRP login that produces access/refresh
tokens. Works for Drive and account operations.

**Cookie (`--cookie-session`):** Performs the full Proton login flow
(SRP + CAPTCHA if needed), then transitions to cookie-based auth.
Required for services that use cookie-scoped sessions (Lumo).

If CAPTCHA is triggered during Bearer login, a Chrome window opens
automatically for solving.

### Multiple accounts

Use `--account <name>` on any command to target a specific account:

```sh
proton --account work account login -u work@proton.me
proton --account personal account login -u personal@proton.me
proton --account work drive ls
```

## Logout

```sh
proton account logout [--force]
```

Revokes the session and removes stored credentials. Use `--force` to
remove credentials even if the revocation API call fails.

## Info

```sh
proton account info
```

Displays account details: username, email addresses, subscription plan,
and storage usage.

## Status

```sh
proton account status
```

Shows session status for all services (account, drive, lumo). Reports
token age, staleness, and whether each service session is active.

## Addresses

```sh
proton account addresses
```

Lists all email addresses associated with the account.

## List

```sh
proton account list
```

Lists all stored accounts in the session store.

## Session Lifecycle

Sessions are stored in the OS keyring (libsecret on Linux, Keychain on
macOS). Token refresh happens automatically via the Proton API's 401
retry mechanism.

Service-specific sessions (drive, lumo) are forked from the account
session on first use. The fork grants service-specific scopes without
requiring re-authentication.

For technical details on the session protocol, see:
- [Authentication & Sessions](authentication-session-management.md)
- [Cookie Auth Flow](cookie-auth-flow.md)
- [Session Fork Protocol](session-fork-protocol.md)
