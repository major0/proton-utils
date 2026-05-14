# Cookie-Based Authentication Flow

## Overview

Proton services like Lumo require cookie-based authentication. The browser
uses a specific login flow that creates an anonymous session, transitions it
to cookies, then performs SRP login within that cookie session. This document
describes the browser's exact flow (from HAR analysis) and the implementation
requirements for the CLI.

## Browser Login Flow

Captured from `lumo.proton.me` HAR (direct Lumo login, no prior account
login). All requests go through `account.proton.me` for login, then fork
to `lumo.proton.me` for the child session.

### Step 1: Anonymous Session

```
POST account.proton.me/api/auth/v4/sessions
```

- No authentication required
- Header: `x-enforce-unauthsession: true`
- Header: `x-pm-appversion: web-account@5.0.368.0`
- Header: `accept: application/vnd.protonmail.v1+json`
- Empty body (`content-length: 0`)
- Returns: `{UID, AccessToken, RefreshToken}`
- Response sets `Session-Id` and `Tag` cookies (`Domain=proton.me`)

### Step 2: Cookie Transition (Anonymous)

```
POST account.proton.me/api/core/v4/auth/cookies
```

- Auth: `Authorization: Bearer <AccessToken from step 1>`
- Header: `x-pm-uid: <UID from step 1>`
- Body: `{UID, RefreshToken, GrantType: "refresh_token", ResponseType: "token", RedirectURI: "https://protonmail.com", State: "<random>"}`
- Response sets `AUTH-<uid>` and `REFRESH-<uid>` cookies
- **After this call, Bearer tokens from step 1 are INVALID**

### Step 3: SRP Login (Cookie Auth)

```
POST account.proton.me/api/core/v4/auth/info
POST account.proton.me/api/core/v4/auth
```

- Auth: Cookie only (`AUTH-<uid>` cookie, no Bearer header)
- Header: `x-pm-uid: <UID>`
- SRP protocol via `go-srp` library
- The `auth/info` response provides SRP parameters (salt, modulus, server ephemeral)
- The `auth` request sends the SRP proof

### Step 4: 2FA (Cookie Auth)

```
POST account.proton.me/api/core/v4/auth/2fa
```

- Auth: Cookie only
- Body: `{TwoFactorCode: "<code>"}`

### Step 5: Account Operations (Cookie Auth)

```
GET account.proton.me/api/core/v4/users
GET account.proton.me/api/core/v4/keys/salts
GET account.proton.me/api/core/v4/addresses
```

- Auth: Cookie only
- Used for key derivation and session setup

### Step 6: Fork Push (Cookie Auth)

```
POST account.proton.me/api/auth/v4/sessions/forks
```

- Auth: Cookie only (`AUTH-<uid>`, no Bearer)
- Body: `{ChildClientID: "web-lumo", Independent: 0, Payload: "<encrypted>"}`
- Headers must include:
  - `origin: https://account.proton.me`
  - `referer: https://account.proton.me/authorize?app=proton-lumo`
  - `x-pm-appversion: web-account@5.0.368.0`
- Returns: `{Selector: "<selector>"}`

### Step 7: Fork Pull

```
GET lumo.proton.me/api/auth/v4/sessions/forks/<selector>
```

- No auth cookies (only `Session-Id`, `Tag` metadata cookies)
- Header: `x-pm-appversion: web-lumo@1.3.3.4`
- Returns: `{UID, AccessToken, RefreshToken, Scopes: [..., "lumo"], Payload}`

### Step 8: Child Cookie Transition

```
POST lumo.proton.me/api/core/v4/auth/cookies
```

- Auth: `Authorization: Bearer <child AccessToken from step 7>`
- Header: `x-pm-uid: <child UID>`
- Header: `x-pm-appversion: web-lumo@1.3.3.4`
- Response sets `AUTH-<child-uid>` and `REFRESH-<child-uid>` cookies
- **After this call, child Bearer tokens are INVALID**

### Step 9: Lumo API (Child Cookie Auth)

```
GET lumo.proton.me/api/lumo/v1/spaces
GET lumo.proton.me/api/lumo/v1/masterkeys
...
```

- Auth: Cookie only (`AUTH-<child-uid>`, no Bearer)
- Header: `x-pm-uid: <child-uid>`
- Header: `x-pm-appversion: web-lumo@1.3.3.4`

## Key Technical Details

### Cookie Domain and Path

The server sets cookies with specific domain and path attributes:

| Cookie | Domain | Path |
|--------|--------|------|
| `AUTH-<uid>` | `proton.me` | `/api/` |
| `REFRESH-<uid>` | `proton.me` | `/api/auth/refresh` |
| `Session-Id` | `proton.me` | `/` |
| `Tag` | (host-only) | `/` |

- `Domain=proton.me` means cookies are sent to all `*.proton.me` subdomains
- `path=/api/` means AUTH cookies are sent to any `/api/*` request
- `path=/api/auth/refresh` means REFRESH cookies are only sent to the refresh endpoint
- Go's `cookiejar` does NOT preserve Domain/Path on `Cookies()` — they must be restored when loading from persistence

### Accept Header

All Proton API requests use the vendor media type:
```
Accept: application/vnd.protonmail.v1+json
```

Not `application/json`. The vendor type may trigger different server behavior.

### App Version Format

```
<clientID>@<version>
```

Examples: `web-account@5.0.368.0`, `web-lumo@1.3.3.4`

The `+proton-utils` build metadata suffix (semver standard) is not used.
Proton Utils sends version strings without any suffix, matching the browser.

### Bearer Token Invalidation

`POST /core/v4/auth/cookies` **invalidates** the Bearer tokens used in the
request. After transition, only cookie auth works. This is a one-way
operation — there is no way to get Bearer tokens back from cookies.

### Scope Grants

The `lumo` scope is granted on the fork pull response when:
1. The fork push uses cookie auth (not Bearer)
2. The fork push is made from a session created on `account.proton.me`
3. The `ChildClientID` is `web-lumo`

The exact mechanism for scope determination is server-side. The Referer
header with `app=proton-lumo` may influence scope grants.

## Implementation Components (Built)

| Component | File | Purpose |
|-----------|------|---------|
| `CookieSession` | `api/account/cookie_session.go` | Cookie-authenticated DoJSON/DoSSE |
| `CookieTransport` | `api/account/cookie_transport.go` | http.RoundTripper that strips Bearer |
| `TransitionToCookies` | `api/account/cookie_session.go` | POST auth/cookies transition |
| `CookieSessionFromForkPull` | `api/account/fork.go` | Child session with CookieTransport |
| `CookieFork` | `api/account/fork.go` | Cookie-aware fork push/pull |
| `CookieLoginSave` | `api/account/cookie_session.go` | Persist cookie session to keyring |
| `CookieSessionRestore` | `api/account/cookie_session.go` | Restore cookie session from keyring |
| `CreateAnonSession` | `api/account/anon_session.go` | POST auth/v4/sessions |
| `loadProtonCookies` | `api/account/cookie_session.go` | Load cookies with Domain=proton.me |

## Implementation Gap

The SRP login (steps 3-4) currently uses `go-proton-api`'s Resty client,
which sends Bearer auth. The browser does SRP login within a cookie session.
To match the browser flow exactly, the SRP login must be implemented using
`CookieSession.DoJSON` with the `go-srp` library for the cryptographic
protocol, bypassing `go-proton-api`'s Resty-based login entirely.

## Architecture Decision: go-proton-api

The cookie login flow can be implemented entirely in `proton-utils`'s `api/account/`
package without forking `go-proton-api`. The approach:

- **Cookie login path** (`--cookie-session`): SRP login via `go-srp` +
  `CookieSession.DoJSON`. Does not use `go-proton-api` for login at all.
  Child sessions use `proton.Manager` with `CookieTransport` (via
  `proton.WithTransport`) for Resty-based calls like GetUser/GetAddresses.

- **Bearer login path** (`--no-cookie-session`): Existing `go-proton-api`
  Resty-based login. No changes needed. Drive operations continue to work.

This avoids forking `go-proton-api`. The `CookieTransport` plugs into the
existing `proton.Manager` via the public `WithTransport` option — no
upstream changes required.

If Proton's cookie-based session model is intentionally designed to limit
non-browser access, upstream PRs to `go-proton-api` for cookie support
would likely not be accepted. Keeping the cookie path in `proton-utils`
isolates this concern.
