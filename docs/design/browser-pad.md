# The browser pad

A pad for the phone's home screen, served as a web page by `tally serve --web`. It lights by the same rules as the hardware pad, runs its keys through tally's action table, and keeps the same privacy rule: no titles, paths or terminal output leave the host.

## Shape

- `internal/web` serves HTTPS on `:47825` (`--web-listen`). The page is `internal/web/static/`, embedded in the binary: `index.html`, `pad.css`, `pad.js` and `lighting.js`, with no build step and no third-party code.
- The page opens `wss://<host>/ws` and speaks protocol v3 (`docs/protocol-v3.md`) minus the `auth` message: the sign-in cookie is the auth, checked before the upgrade. tally answers `hello` then `state`, as for any v3 client.
- `/ws` hands the socket to `remote.Server.Attach`, so a browser session is an ordinary v3 session: the same action table, the same herdr write allowlist, one action at a time, acks, a state frame on change and every 5 s. `--web` works without `--remote`; the remote server is built either way and only listens with `--remote`.

## Sign-in

- One password per host. There is always one: the first `tally web` or `tally install-agent --web` generates 20 characters from a look-alike-free alphabet (about 100 bits) and prints it once, and `tally serve --web` generates one itself, without logging it, if none is set. `tally web --set-password` picks your own (12+ characters). tally stores an argon2id hash (RFC 9106's second recommended parameters: 64 MiB, 3 passes, 4 lanes) in `~/.config/tally/web.json`, mode 0600. Setting it signs every browser out.
- A successful sign-in gives the browser 32 random bytes as the `__Host-tally` cookie: `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`, no `Domain`, 90 days, renewed while the pad is used; a browser unseen for 90 days is signed out on the server too. tally stores only the cookie's SHA-256, a label from the User-Agent ("iphone safari"), the source address and two timestamps.
- Limits: 5 failed sign-ins per source address and 20 overall per minute, then HTTP 429. Password checks run one at a time (each takes 64 MiB), and each re-checks the limit when its turn comes, so requests sent at once get no extra guesses. The overall limit covers passwords only: a 256-bit sign-in code can't be guessed, so a flood of bad passwords can't stop QR and home screen sign-ins. Sign-in and sign-out posts must come from the page's own origin; the WebSocket refuses any other `Origin`.
- Single-use sign-in codes stand in for typing the password. `tally web` (and `install-agent --web`) prints a QR code of `https://<name>:47825/?code=<32 random bytes>`, valid for 15 minutes; the page itself asks for a one-hour code (`POST /install-code`) and puts it in its own address while the install card is open (it leaves the address when the card closes), because the manifest has no `start_url`, so the home screen copy opens that address, and on iOS that copy has its own cookie jar. `GET /?code=` from a browser without a session redeems the code, signs it in and redirects to `/`; a signed-in browser is just redirected, leaving the code for the home screen copy. tally stores only each code's SHA-256, its expiry and, for a page's code, the browser that asked for it. A browser holds at most three; signing it out or revoking it voids them. A bad code counts as a failed sign-in from its address; a new password or `--revoke all` voids every code. Redeeming a code and recording the browser happen in one write, as do the password sign-in and its check that the password hasn't just changed. The password never goes in a URL, where it would sit in history.
- `web.json` is read-modify-written under `flock` on `web.lock`, since `tally serve` and the `tally web` CLI both write it. `tally serve --web` also records its `--web-listen` and `--web-cert` there, so `tally web` puts the right port in its QR code and `tally doctor` probes the right address.
- `tally web --list` shows signed-in browsers; `tally web --revoke ID|all` signs them out. tally rereads `web.json` every second and closes the session with status 1008, and the page goes back to the sign-in screen.
- Every response carries a strict CSP (`default-src 'none'`, scripts and styles from the page's origin only, `connect-src` its own `wss://`), `frame-ancestors 'none'`, `nosniff` and `Cache-Control: no-store`. No HSTS: it would apply to every port on the MagicDNS name and break plain-http services there.

## Certificates

A browser can't pin a self-signed certificate, so by default the listener presents a certificate browsers already trust: Tailscale's Let's Encrypt certificate for this machine's MagicDNS name.

- `web.Tailscale` runs `tailscale cert --cert-file … --key-file … <name>.ts.net` into `~/.config/tally/web/` at start and daily after, retrying every minute while Tailscale is down (it often starts after tally at login). `tailscale cert` reuses its cached certificate until renewal is due. Handshakes fail until the first certificate arrives.
- It needs MagicDNS and HTTPS certificates on in the tailnet; on Linux the user also needs `sudo tailscale set --operator=$USER`. Issuing the certificate publishes the `*.ts.net` name in Certificate Transparency logs.
- tally looks for the `tailscale` CLI on `PATH`, then in Homebrew's, the Mac app's and NixOS's locations, since a LaunchAgent or systemd unit doesn't get the login shell's `PATH`.
- `--web-cert self` keeps the name out of those logs: tally runs its own certificate authority (`web.Self`) and each device trusts it once (`tally web --ca` writes it as DER for AirDrop or `security add-trusted-cert -p ssl`). It does not reuse the pairing certificate, which phones pin by fingerprint and which no browser accepts: browsers need a CA they trust, a SAN for the name, and, on Apple platforms, a server certificate valid no more than 825 days.
  - Files live in `~/.config/tally/web/self/<MagicDNS name>/`: `ca.pem` and `server.pem`, each holding a certificate and its key so the pair can't tear, mode 0600. A directory per name means switching tailnets and back finds the CA devices already trust. A file that won't load is renamed `*.replaced-<time>`, never overwritten; `ca.lock` keeps `tally serve` and `tally web --ca` from making two CAs.
  - The CA is ECDSA P-256, valid ten years, `pathlen:0`, with server-auth EKU only and critical name constraints: only the MagicDNS name, no IP ranges, and email and URI names only under `.invalid`. Even with its key, nobody can make a certificate a client accepts for another site, an IP, signed mail or client auth. Apple still accepts a code-signing certificate under an anchor trusted for every policy, which is why the docs trust it for SSL only (`-p ssl`; iOS's Certificate Trust Settings is SSL trust).
  - tally replaces the CA when it can no longer outlast a new server certificate (`CAReplaceAt`: 397 days before expiry, so about nine years in); devices must then trust the new one, and `tally doctor` warns 60 days ahead.
  - The server certificate has the MagicDNS name as its only SAN, server auth only, and is valid 397 days (under Apple's 398-day limit for public certificates too). It is reissued with 30 days left, or when it no longer verifies against the CA for the current name.
  - `Run` refreshes daily (the `tailscale status` call times out after 10 s) and also checks every minute whether the CA file changed, so a CA from `tally web --ca` is served at once.
  - `tally doctor` verifies the server certificate against the CA for the current name and reports its expiry and the CA's replacement date.

The listener binds every interface by default, so on the LAN the page is reachable by address too, but only the MagicDNS name matches the certificate.

## Browser specifics

- TALK is a text box over the DRIVE keys. Dictate with the keyboard's mic, which on iOS runs on-device for most languages; return types the text into the agent (`insert_text`), and ENTER sends it.
- No haptics on iOS (`navigator.vibrate` only exists on Android). Keep-awake uses the Screen Wake Lock API where the browser has it.
- One host per page: no host switcher. Each host's URL is its own pad.
- The page fills exactly the visible height (`100dvh`, so Safari's toolbars count) and the DRIVE rows share what's left, 44 to 96 px each, with smaller tiles on short screens; it scrolls only when even that doesn't fit.
- On first run in a browser tab it recommends the home screen once; settings has the same button later.
- Browsers can't send WebSocket pings, so the page treats 8 s without a message as stale (tally sends state every 5 s) and reconnects with backoff, immediately when the page becomes visible again. The header trace shows state frames only.
- Keyboard: each key's letter (Esc, Tab and Return included), the arrows, `1`–`6` for slots, `0` for ATTN, `t` for TALK, and `N`/`R` (shifted) for NEW and RESUME, since browsers keep ⌘N and ⌘R.

## Lighting

`static/lighting.js` ports the hardware pad's `render.py` rule for rule, keyed by action instead of OLED label. `static/lighting_test.mjs` holds the same cases as vibe-macropad's `tests/test_render.py`; `go test ./internal/web` runs it when node is installed. A lighting change goes in every client and its tests.
