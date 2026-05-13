# Human Verification / CAPTCHA

Proton uses a custom CAPTCHA system (replacing the earlier Google reCAPTCHA) to gate
certain API operations — most notably authentication from third-party clients.
When the API returns error code **9001**, the client must complete a human-verification
challenge before retrying the request.

## How It Works

1. The API responds with HTTP 422 and a JSON body containing error code 9001 and
   `HumanVerificationMethods` / `HumanVerificationToken` in the `Details` field.
2. The response also includes a `WebUrl` field:
   `https://verify.proton.me/?methods=captcha&token=<HumanVerificationToken>`.
3. The client opens this URL in a browser. The user solves the CAPTCHA challenge
   on Proton's servers.
4. The solved CAPTCHA produces a **composite token** in the format:
   `<HumanVerificationToken>:<solvedCaptchaToken>`. This is NOT the same as the
   original `HumanVerificationToken` — it includes a suffix derived from the
   CAPTCHA solution.
5. The client must capture this composite token and send it in the
   `x-pm-human-verification-token` header on the retry request.

## Token Format (Critical)

The `x-pm-human-verification-token` header value for a solved CAPTCHA is:

```
<originalToken>:<specialToken><captchaSessionToken>
```

Where:
- `<originalToken>` is the `HumanVerificationToken` from the 9001 response
- `<specialToken>` is extracted from the CAPTCHA page's JavaScript
- `<captchaSessionToken>` is the session token from the CAPTCHA init API

Simply passing the original `HumanVerificationToken` unchanged will result in
error code **12087** ("CAPTCHA validation failed") because the server expects
the composite token proving the challenge was solved.

## How verify.proton.me Communicates the Result

When the CAPTCHA is solved on `verify.proton.me`, the page uses `postMessage`
to send the composite token back to the parent/opener window. A headless CLI
client that opens the URL in a browser has no way to receive this `postMessage`.

### Approaches for CLI Clients

1. **Embedded browser / WebView** — Intercept the `postMessage` from the
   CAPTCHA iframe to capture the composite token. This is what Proton's own
   GUI clients (bridge GUI, web client) do.

2. **Manual token entry** — Print the `verify.proton.me` URL, instruct the
   user to solve the CAPTCHA, then have them paste the solved token from the
   browser's developer console or URL bar. This is fragile.

3. **Programmatic CAPTCHA solving** — Implement the CAPTCHA protocol directly:
   fetch the challenge from `/captcha/v1/api/init`, solve the proof-of-work
   and image challenge, submit via `/captcha/v1/api/validate`, and construct
   the composite token. See the `gravilk/protonmail-documented` repository
   for a reference implementation (may be outdated).

4. **Local HTTP callback** — Start a local HTTP server, construct a wrapper
   page that embeds the `verify.proton.me` iframe, listen for the
   `postMessage` callback, and capture the composite token automatically.

## Key Observations

- Third-party clients (rclone, proton-utils) frequently hit CAPTCHA during initial
  login because Proton's anti-abuse system flags non-browser user agents.
- Encryption keys must already exist on the account (i.e. the user must have logged
  in via the web client at least once) before a headless client can authenticate.
- The proton-bridge CLI implementation (`internal/hv/hv.go` and
  `internal/frontend/cli/accounts.go`) prints the `verify.proton.me` URL and
  waits for ENTER, then retries with the original `hvDetails` unchanged. This
  approach has the same 12087 failure — it does NOT capture the composite token.
  Bridge's GUI frontend likely handles this differently via WebView/postMessage.
- HV behavior may differ between API entry points (`mail.proton.me/api` vs
  `mail-api.proton.me`). The ElectronMail project reported that HV only worked
  on certain API entry points (issue #419).

## References

- [rclone #7967 — proton-drive remote fails due to captcha](https://github.com/rclone/rclone/issues/7967)
- [rclone forum — Captcha error when using Proton Drive](https://forum.rclone.org/t/captcha-error-when-using-proton-drive/47879)
- [rclone forum — Rclone can't connect to Proton Drive (CAPTCHA errors)](https://forum.rclone.org/t/rclone-can-t-connect-to-proton-drive-captcha-errors/52302)
- [proton-bridge `internal/hv` package](https://pkg.go.dev/github.com/ProtonMail/proton-bridge/v3/internal/hv)
- [ElectronMail #419 — Human Verification broken captcha workflow](https://github.com/vladimiry/ElectronMail/issues/419)
- [gravilk/protonmail-documented — Proton CAPTCHA documented and solved](https://github.com/gravilk/protonmail-documented)
- [gravilk — ProtonMail captcha solving function (gist)](https://gist.github.com/gravilk/1299aefa9324e33d1d84e0ad23b6f3ad)
- [Proton blog — Introducing Proton CAPTCHA](https://www.proton.me/blog/proton-captcha)
- [ProtonMail/proton-mail #78 — Enable HV endpoints for app.protonmail.ch](https://github.com/ProtonMail/proton-mail/issues/78)
- [WebClients #242 — ProtonMail includes Google Recaptcha for Login](https://github.com/ProtonMail/WebClients/issues/242)
- [HN discussion — ProtonMail includes Google Recaptcha for login](https://news.ycombinator.com/item?id=27326243)
