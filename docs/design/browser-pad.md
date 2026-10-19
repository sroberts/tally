# The browser pad

The soft pad's screen as a web page, served by `tally serve --web`, for a phone without the vibepad app. Same lighting, same keys, same actions, same privacy rule: no titles, paths or terminal output leave the host.

## Shape

- `internal/web` serves HTTPS on `:47825` (`--web-listen`). The page is `internal/web/static/`, embedded in the binary: `index.html`, `pad.css`, `pad.js` and `lighting.js`, with no build step and no third-party code.
- The page opens `wss://<host>/ws` and speaks protocol v3 (`docs/protocol-v3.md`) minus the `auth` message: the sign-in cookie is the auth, checked before the upgrade. tally answers `hello` then `state`, exactly as for the app.
- `/ws` hands the socket to `remote.Server.Attach`, so a browser session is a phone session: the same action table, the same herdr write allowlist, one action at a time, acks, a state frame on change and every 5 s. `--web` works without `--remote`; the remote server is built either way and only listens with `--remote`.

## Sign-in

- One password per host. There is always one: the first `tally web` or `tally install-agent --web` generates 20 characters from a look-alike-free alphabet (about 100 bits) and prints it once, and `tally serve --web` generates one itself, without logging it, if none is set. `tally web --set-password` picks your own (12+ characters). tally stores an argon2id hash (RFC 9106's second recommended parameters: 64 MiB, 3 passes, 4 lanes) in `~/.config/tally/web.json`, mode 0600. Setting it signs every browser out.
- A successful sign-in gives the browser 32 random bytes as the `__Host-tally` cookie: `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`, no `Domain`, 90 days, renewed while the pad is used. tally stores only the cookie's SHA-256, a label from the User-Agent ("iphone safari"), the source address and two timestamps.
- Limits: 5 failed sign-ins per source address and 20 overall per minute, then HTTP 429; password checks run one at a time (each takes 64 MiB); sign-in and sign-out posts must come from the page's own origin; the WebSocket refuses any other `Origin`.
- Single-use sign-in codes stand in for typing the password. `tally web` (and `install-agent --web`) prints a QR code of `https://<name>:47825/?code=<32 random bytes>`, valid for 15 minutes; the page itself asks for a one-hour code (`POST /install-code`) and puts it in its own address before you add it to the home screen, because the manifest has no `start_url`, so the home screen copy opens that address, and on iOS that copy has its own cookie jar. `GET /?code=` from a browser without a session redeems the code, signs it in and redirects to `/`; a signed-in browser is just redirected, leaving the code for the home screen copy. tally stores only each code's SHA-256 and expiry. A bad code counts as a failed sign-in; a new password or `--revoke all` voids every code. The password never goes in a URL, where it would sit in history.
- `web.json` is read-modify-written under `flock` on `web.lock`, since `tally serve` and the `tally web` CLI both write it.
- `tally web --list` shows signed-in browsers; `tally web --revoke ID|all` signs them out. Like phone revocation, tally rereads `web.json` every second and closes the session with status 1008, and the page goes back to the sign-in screen.
- Every response carries a strict CSP (`default-src 'none'`, scripts and styles from the page's origin only, `connect-src` its own `wss://`), `frame-ancestors 'none'`, `nosniff`, HSTS and `Cache-Control: no-store`.

## Certificates

A browser can't pin a self-signed certificate the way the app does, so by default the listener presents a certificate browsers already trust: Tailscale's Let's Encrypt certificate for this machine's MagicDNS name.

- `web.Tailscale` runs `tailscale cert --cert-file … --key-file … <name>.ts.net` into `~/.config/tally/web/` at start and daily after, retrying every minute while Tailscale is down (it often starts after tally at login). `tailscale cert` reuses its cached certificate until renewal is due. Handshakes fail until the first certificate arrives.
- It needs MagicDNS and HTTPS certificates on in the tailnet; on Linux the user also needs `sudo tailscale set --operator=$USER`. Issuing the certificate publishes the `*.ts.net` name in Certificate Transparency logs.
- tally looks for the `tailscale` CLI on `PATH`, then in Homebrew's, the Mac app's and NixOS's locations, since a LaunchAgent or systemd unit doesn't get the login shell's `PATH`.
- `--web-cert self` presents tally's own certificate (the one phones pin). Browsers warn until it is trusted on the device; it is for LAN-only setups and local testing.

The listener binds every interface by default, so on the LAN the page is reachable by address too, but only the MagicDNS name matches the certificate.

## Differences from the app

- TALK is a text box over the DRIVE keys. Dictate with the keyboard's mic, which on iOS runs on-device for most languages; return types the text into the agent (`insert_text`), and ENTER sends it.
- No haptics on iOS (`navigator.vibrate` only exists on Android). Keep-awake uses the Screen Wake Lock API where the browser has it.
- One host per page: no host switcher. Each host's URL is its own pad.
- The page fills exactly the visible height (`100dvh`, so Safari's toolbars count) and the DRIVE rows share what's left, 44 to 96 px each, with smaller tiles on short screens; it scrolls only when even that doesn't fit.
- On first run in a browser tab it recommends the home screen once; settings has the same button later.
- Browsers can't send WebSocket pings, so the page treats 8 s without a message as stale (tally sends state every 5 s) and reconnects with backoff, immediately when the page becomes visible again. The header trace shows state frames only.
- Keyboard: the app's keys, plus `1`–`6` for slots, `0` for ATTN, `t` for TALK, and `N`/`R` (shifted) for NEW and RESUME, since browsers keep ⌘N and ⌘R.

## Lighting

`static/lighting.js` ports vibepad's `Lighting.swift` and `Readout.swift` rule for rule. `static/lighting_test.mjs` holds the same cases as vibepad's `LightingTests.swift` (themselves ported from the pad's `test_render.py`); `go test ./internal/web` runs it when node is installed. A lighting change goes in both apps and both test files.
