# tally

The host daemon behind the vibepads. It watches [herdr](https://herdr.dev)'s agents and keeps two kinds of client in step with them:

- **[vibe-macropad](https://github.com/sroberts/vibe-macropad)**, the hardware pad: tally sends state frames over the pad's second USB serial port (protocol v2) and the pad lights its keys. The pad types through USB HID; tally never writes to herdr for it.
- **[vibepad](https://github.com/sroberts/vibepad)**, the iPhone soft pad: tally serves a pinned-TLS WebSocket (protocol v3), sends the same state, and executes the phone's actions directly against herdr's socket.

Both can run at once off the same state engine.

```bash
go build -o ~/.local/bin/tally ./cmd/tally
tally install-agent   # run tally serve in the background, now and at login
tally doctor          # check herdr, its settings, the background agent, the pad and the remote listener
tally status          # herdr's agents in slot order
```

`tally serve` runs the daemon in a terminal instead. On macOS it is a LaunchAgent logging to `~/Library/Logs/tally.log`; on Linux, a systemd user unit logging to the journal (`journalctl --user -u tally`). The log records connects, disconnects and errors; `--verbose` adds each frame and phone action. `tally uninstall-agent` removes the background agent.

### The soft pad

```bash
tally pair                          # shows a QR code; scan it with vibepad
tally install-agent --remote        # serve phones on :47824 as well as the pad
tally pair --list                   # paired phones
tally pair --revoke phone           # unpair one and drop its live connection
tally act approve_once --dry-run    # any soft pad action from the terminal
```

The phone pins tally's certificate from the QR code and authenticates with a token only tally's QR ever showed, so any network path is fine: the same Wi-Fi, the phone's hotspot, or Tailscale. `--listen` narrows the listener (for example to the Tailscale address only); `--no-device` skips looking for the pad. tally executes the phone's actions directly against herdr's socket, so no Accessibility permission or keystroke injection is involved, and DRIVE actions only ever reach agent panes. The wire format is [`docs/protocol-v3.md`](docs/protocol-v3.md).

Slot codes, shared by every client:

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
- [`docs/design/softpad.md`](docs/design/softpad.md): the soft pad, actions, remote listener, pairing, protocol v3.
- [`docs/m0-herdr-input.md`](docs/m0-herdr-input.md): which herdr methods and key names the action table uses.
- [`docs/protocol-v3.md`](docs/protocol-v3.md): the soft pad's wire format, as built.

## Development

```bash
go test ./...
```

Work follows GitHub Flow: branch, open a PR, merge with a merge commit.

## License

MIT; see [`LICENSE`](LICENSE).
