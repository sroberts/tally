# tally

The host daemon for your agent pads. It watches [herdr](https://herdr.dev)'s agents and keeps its clients in step with them:

- **[vibe-macropad](https://github.com/sroberts/vibe-macropad)**, the hardware pad: tally sends state frames over the pad's second USB serial port (protocol v2) and the pad lights its keys. The pad types through USB HID; tally never writes to herdr for it.
- **The browser pad**, a pad on your phone's home screen: `tally serve --web` serves it over HTTPS behind a password, shows the same state, and runs its keys directly against herdr's socket.

Both can run at once off the same state engine.

```bash
go build -o ~/.local/bin/tally ./cmd/tally
tally install-agent   # run tally serve in the background, now and at login
tally doctor          # check herdr, its settings, the background agent, the pad and the browser pad
tally status          # herdr's agents in slot order
```

`tally serve` runs the daemon in a terminal instead. On macOS it is a LaunchAgent logging to `~/Library/Logs/tally.log`; on Linux, a systemd user unit logging to the journal (`journalctl --user -u tally`). The log records connects, disconnects and errors; `--verbose` adds each frame and action. `tally uninstall-agent` removes the background agent; `--no-device` skips looking for the hardware pad.

## The browser pad

A phone-sized control surface for your agents: six agent tiles with an attention badge, twelve DRIVE keys (esc, mode, rewind, yes, always, no, tab, log, compact, enter, talk, bg) aimed at the focused agent, and a row of tools (new, resume, zoom, last, scroll). Keys light by the focused agent's state, so the ones that matter right now stand out.

### Getting to it

1. **Tailscale on both ends.** Install [Tailscale](https://tailscale.com) on the machine running herdr and on your phone, signed in to the same tailnet. In the [admin console](https://login.tailscale.com/admin/dns), turn on **MagicDNS** and **HTTPS certificates**. On Linux, also let tally ask for a certificate without root: `sudo tailscale set --operator=$USER`.
2. **Install tally** with the browser pad on:
   ```bash
   go build -o ~/.local/bin/tally ./cmd/tally
   tally install-agent --web
   ```
   This starts tally in the background, generates a password and prints it once, and prints a QR code. tally fetches the certificate for `https://<machine>.<tailnet>.ts.net:47825/` itself within a minute and renews it.
3. **Scan the QR code** with your phone's camera. It opens the pad and signs that browser in; the code works once, for 15 minutes. Run `tally web` any time for a fresh one.
4. **Add it to the home screen.** The first time, the pad offers to: in Safari, tap Share → Add to Home Screen → Add, then open **tally** from the home screen. It runs full screen and signs itself in.
5. **Check** with `tally doctor`: the `web`, `web password` and `web cert` lines should read `ok`.

### Using it

- Tap a tile to focus that agent; swipe across the tiles for the next or previous one; **attn** jumps to the agent that has waited longest.
- DRIVE keys always act on the agent named in the `02 drive` header. Approvals pulse ochre when the focused agent needs you; **enter** turns green when it's ready.
- **talk** opens a text box: type, or tap the keyboard's mic to dictate, then return types the text into the agent and **enter** sends it.
- With a keyboard: the letters on the keys (`y`, `a`, `n`, `m`, `r`, `o`, `c`, `b`, `z`, `l`), Esc, Tab, Return, the arrows, `1`–`6` for tiles, `0` for attn and `t` for talk.

### Managing access

```bash
tally web                    # a fresh sign-in QR code (and link)
tally web --set-password     # pick your own password (12+ characters); signs every browser out
tally web --list             # signed-in browsers
tally web --revoke 3f9a01c2  # sign one out (or --revoke all); its pad closes within a second
```

The browser pad never runs without a password, and the password never goes in a URL. tally keeps an argon2id hash of it, plus hashes of each browser's cookie and each outstanding sign-in code, in `~/.config/tally/web.json`. Failed sign-ins are limited per address and overall. No agent titles, paths or terminal output leave the machine: the pad sees only slot states and pane ids, and its keys can only reach agent panes.

Issuing the Tailscale certificate publishes the machine's `*.ts.net` name in public Certificate Transparency logs. To avoid that, `--web-cert self` uses tally's own certificate instead (browsers warn until it is trusted on the device). `--web-listen` narrows the listening address.

`tally act approve_once --dry-run` runs any pad action from the terminal, printing the herdr calls instead of making them.

## Slot codes

Shared by every client:

| Code | Means |
| --- | --- |
| `N` | Needs you: a permission prompt or question is waiting |
| `G` | Done: finished a turn you haven't looked at |
| `W` | Working |
| `R` | Ready: idle, and you've seen its result |
| `U` | Unknown: herdr can't tell yet |
| `null` | No agent in that slot |

## Design

- [`docs/design/tally-macropad.md`](docs/design/tally-macropad.md): the daemon, slot ordering, serial protocol v2.
- [`docs/design/browser-pad.md`](docs/design/browser-pad.md): the browser pad, its sign-in and certificates.
- [`docs/m0-herdr-input.md`](docs/m0-herdr-input.md): which herdr methods and key names the action table uses.

## Development

```bash
go test ./...
node --test internal/web/static/lighting_test.mjs   # also run by go test when node is installed
```

Work follows GitHub Flow: branch, open a PR, merge with a merge commit.

## License

MIT; see [`LICENSE`](LICENSE).
