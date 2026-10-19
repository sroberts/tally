# tally

tally watches [herdr](https://herdr.dev)'s Claude Code agents and keeps your pads in step with them. It runs in the background on the machine where herdr runs and drives two pads:

- **[vibe-macropad](https://github.com/sroberts/vibe-macropad)**, the hardware pad. Its keys light by each agent's state.
- **The browser pad**, a pad for your phone's home screen. It shows the same state, and its keys act on your agents directly.

Use either one or both at once.

## Install

You need [Go](https://go.dev/dl/) 1.27 or later and a running herdr.

```bash
git clone https://github.com/sroberts/tally && cd tally
go build -o ~/.local/bin/tally ./cmd/tally
```

## Start it

**Hardware pad only:**

```bash
tally install-agent   # runs tally in the background, now and at login
tally doctor          # checks herdr, the background agent and the pad
```

**With the browser pad:** set up [Tailscale](https://tailscale.com) first (below), then:

```bash
tally install-agent --web
```

This prints a generated password once and a QR code. Scan the code with your phone's camera to open the pad already signed in.

## The browser pad

### Set up Tailscale

1. Install Tailscale on the machine running herdr and on your phone, signed in to the same tailnet.
2. In the [admin console](https://login.tailscale.com/admin/dns), turn on **MagicDNS** and **HTTPS certificates**.
3. On Linux, also run `sudo tailscale set --operator=$USER` so tally can fetch its certificate without root.

tally fetches the certificate for `https://<machine>.<tailnet>.ts.net:47825/` itself within a minute, and renews it.

### Open it on your phone

1. **Scan the QR code** from `tally install-agent --web`, or run `tally web` for a fresh one. A code works once, for 15 minutes.
2. **Add it to the home screen** when the pad offers to. In Safari: Share → Add to Home Screen → Add. Then open **tally** from the home screen; it runs full screen and stays signed in.
3. If something's off, `tally doctor` should show `ok` on its `web`, `web password` and `web cert` lines.

### Use it

- **Tiles** show your agents. Tap one to focus it, swipe across them for the next or previous agent, and tap **attn** to jump to the agent that has waited longest.
- **DRIVE keys** (esc, mode, rewind, yes, always, no, tab, log, compact, enter, talk, bg) act on the agent named in the `02 drive` header. They light when they matter: the approval keys pulse when the agent needs you, and **enter** turns green when it's ready.
- **talk** opens a text box. Type, or dictate with the keyboard's mic. Return types the text into the agent, then **enter** sends it.
- **With a keyboard,** each key's letter works, plus Esc, Tab, Return, the arrows, `1`–`6` for tiles, `0` for attn and `t` for talk.

### Manage access

```bash
tally web                    # a fresh sign-in QR code
tally web --set-password     # pick your own password (12+ characters); signs every browser out
tally web --list             # signed-in browsers
tally web --revoke 3f9a01c2  # sign one out, or --revoke all
```

The pad always has a password, and the password never goes in a URL. Repeated wrong guesses are slowed down. Your agents' titles, paths and terminal output never leave the machine: the pad sees only each agent's state.

Getting the Tailscale certificate publishes the machine's `*.ts.net` name in public Certificate Transparency logs. To avoid that, add `--web-cert self` to use tally's own certificate instead; browsers warn until you trust it on the device. `--web-listen ADDR` narrows where the pad listens (default `:47825`).

## Commands

| Command | Does |
| --- | --- |
| `tally install-agent [--web]` | Run tally in the background, now and at login |
| `tally uninstall-agent` | Stop and remove the background agent |
| `tally doctor` | Check herdr, the background agent, the pad and the browser pad |
| `tally status` | List herdr's agents in slot order |
| `tally web` | Show a sign-in QR code for the browser pad |
| `tally serve [--web]` | Run tally in this terminal instead of the background |

`--no-device` skips looking for the hardware pad. Logs go to `~/Library/Logs/tally.log` on macOS and to the journal on Linux (`journalctl --user -u tally`).

## Agent states

| Code | Means |
| --- | --- |
| `N` | Needs you: a permission prompt or question is waiting |
| `G` | Done: finished a turn you haven't looked at |
| `W` | Working |
| `R` | Ready: idle, and you've seen its result |
| `U` | Unknown: herdr can't tell yet |

## Contributing

See [`docs/developer.md`](docs/developer.md) for building, testing, the design docs and how changes land.

## License

MIT; see [`LICENSE`](LICENSE).
