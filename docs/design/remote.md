# Remote clients

`tally serve --remote` lets a paired phone stand in for the MacroPad when you travel. It shows agent status and drives Claude Code through tally, which executes every action directly against herdr's socket API. It works the same on macOS and Linux because no OS-level keystroke injection exists anywhere in the design. The wire format is [`docs/protocol-v3.md`](../protocol-v3.md).

Three decisions keep it simple and reliable:

1. **The host executes semantic actions through herdr, not keystrokes through the OS.** herdr exposes `pane.send_keys`, `pane.send_text` and `pane.focus` on its socket. The client sends `approve_once`; tally sends key `1` to that agent's pane. No Accessibility permission on macOS, no uinput on Linux, no herdr prefix chords, no keybinding config, no dependency on which window has focus.
2. **Transport is TLS WebSocket over whatever IP path exists, with pinned trust from a QR pairing.** Claude Code needs internet, so whenever you can vibecode, the phone and laptop share an IP path: same Wi-Fi, the phone's hotspot, or Tailscale. Bluetooth adds macOS TCC friction, flaky BlueZ behavior and a custom reconnect stack for a case (no network at all) where Claude Code doesn't run anyway.
3. **The client is thin.** tally holds all state and all key knowledge; herdr does all terminal I/O. The client sends an action name and a pane id; tally validates it and calls herdr, which writes to that pane's PTY.

The hardware pad keeps working unchanged, with its read-only herdr client and USB HID keystrokes. Every client can run at once off the same state engine.

## Transport, discovery and security

A client holds one TLS 1.3 WebSocket to tally on port 47824. Trust comes from a QR code scanned once, so the network path never matters for security.

### Paths

At pairing, tally records every endpoint it can be reached on. On connect, a client races all of them in parallel and keeps the first that completes TLS with the pinned certificate.

| Path | When it wins | Typical latency |
| --- | --- | --- |
| Bonjour on the local network (`_tally._tcp`) | Same Wi-Fi, or laptop on the phone's Personal Hotspot | 5–20 ms |
| Tailscale MagicDNS name | Hotel Wi-Fi with client isolation, phone on cellular | 10–150 ms (direct vs DERP relay) |
| Manual host:port | Anything else | varies |

### Pairing

1. `tally pair` generates a self-signed ECDSA P-256 certificate (once) and a fresh 256-bit token.
2. It prints a QR code in the terminal containing `{v, host_id, host_name, cert_sha256, token, endpoints[]}`.
3. The client scans it and keeps the record in the device's secure storage.
4. tally stores only `sha256(token)`, a label and a created timestamp in `~/.config/tally/remote.json` (mode 0600).
5. `tally pair --list` shows paired phones. `tally pair --revoke <label>` removes one and drops its live connection.

### Authentication on every connection

- The client pins the certificate fingerprint from the QR code and rejects anything else. No CA, no hostname check.
- The client's first message is `auth` with the token. tally compares hashes in constant time.
- tally closes the socket if auth doesn't arrive within 2 s or fails. It allows 5 failures per source IP per minute.
- No other message is accepted before auth succeeds.

A remote client has write access to agent panes, and text in a Claude Code prompt is one approval away from code execution. Pinned TLS plus a revocable token keeps that capability limited to your phone on any network, including hotel Wi-Fi. The listener binds to all interfaces by default; `--listen` restricts it (for example to the Tailscale interface only).

## Action model

The client sends action names and a target pane id. tally owns the single table that turns each action into herdr calls (`internal/actions`). The client never encodes a key and never sends raw keys.

### Targeting

- Every DRIVE action carries `target`: the pane id of the agent the client shows as focused.
- tally rejects the action if that pane no longer exists or isn't an agent. It does not require the pane to be focused in herdr, so an approval always lands on the agent you saw, even if focus moved a moment earlier.
- DRIVE actions never go to a plain shell pane. `/compact` typed into zsh is a bug this rule prevents.
- Because actions target panes, not windows, they work while the laptop's browser or editor has focus.

### DRIVE actions (to target)

| Action | Key | herdr calls |
| --- | --- | --- |
| `interrupt` | ESC | `send_keys esc` |
| `cycle_mode` | MODE | `send_keys shift+tab` |
| `rewind` | REWIND | `send_keys esc`, sleep 80 ms, `send_keys esc` |
| `approve_once` | YES | `send_keys 1` |
| `approve_always` | ALWAYS | `send_keys 2` |
| `deny` | NO | `send_keys 3` |
| `autocomplete` | TAB | `send_keys tab` |
| `transcript` | LOG | `send_keys ctrl+o` |
| `compact` | COMPACT | `send_text "/compact"`, `send_keys enter` |
| `submit` | ENTER | `send_keys enter` |
| `background` | BG | `send_keys ctrl+b` (straight to the PTY, so no prefix collision) |
| `scroll_up` / `scroll_down` | ▲ / ▼ | `send_keys up` / `send_keys down` |
| `insert_text` | TALK | `send_text <text>` (no Enter; you press ENTER) |

### FLEET actions (no target, or a slot)

| Action | Key | Host behavior |
| --- | --- | --- |
| `focus_slot n` | A1–A6 | `pane.focus` on `agents[n-1].pane_id` from the current snapshot |
| `focus_attention` | ATTN | Focus the agent blocked longest (`blocked_since` oldest) |
| `next_agent` / `prev_agent` | swipe | Focus the neighbor of the focused agent in `agents[]`, wrapping |
| `last_pane` | LAST | Focus the previous focused pane tally tracked across snapshots |
| `zoom` | ZOOM | `pane.zoom` on the focused pane |
| `new_claude` | NEW | `tab create --cwd <focused pane cwd> --focus`, then run `claude` in the new pane |
| `resume_claude` | RESUME | Same, running `claude --resume` |

NEW and RESUME read the focused pane's cwd with `pane.get` inside the handler. The cwd never enters the state model, a frame, or a log line.

### Write allowlist

The herdr client gains a second allowlist, active only when the remote listener or the browser pad runs: `pane.send_keys`, `pane.send_text`, `pane.send_input`, `pane.focus`, `pane.zoom`, `pane.get`, `tab.create`. Running a command accepts only the two fixed commands above. Every other write method stays refused.

### Cadence

- tally sends state on change (50 ms debounce), on auth success, and every 5 s. State frames are full snapshots, never deltas.
- Clients drop any action they can't write within 500 ms of the tap and never retry one, because `approve_once` sent twice can approve a second prompt.

## Host daemon

Remote clients extend tally rather than adding a second daemon. The actor, snapshot logic, slot mapping and herdr client stay as built.

| Package | Role |
| --- | --- |
| `internal/actions` | The action table: name → herdr calls. Validates target, enforces agent-only DRIVE targets, runs the REWIND gap, reads cwd for NEW/RESUME. Pure apart from a `herdr.Writer` interface, so tests use a fake. |
| `internal/remote` | TLS listener, WebSocket sessions, auth, per-IP rate limit, v3 frame encoding, Bonjour advertisement. Subscribes to the actor's frames exactly like `device.Link`. |
| `internal/pairing` | Certificate generation, token issue and revoke, `remote.json`, terminal QR rendering. |

`internal/herdr` gains a `Writer` type built only by `tally serve --remote`/`--web` and `tally act`. It wraps the write allowlist. The read-only `Client` and its allowlist stay untouched, so the serial path keeps its guarantee. `internal/slots` adds a pane per slot, `fpane`, and `blocked_since`, tracked per pane id in the actor across snapshots.

`tally act` runs any action from the terminal through the same table, as a test harness and a debugging tool.

Dependencies: `github.com/coder/websocket`, `github.com/grandcat/zeroconf` for Bonjour on both OSes, `github.com/mdp/qrterminal/v3` for the pairing QR code, and the standard library's `crypto/tls`, `crypto/ecdsa` and `crypto/subtle` for everything else.

### macOS

- LaunchAgent as before (`local.tally`), with `--remote` in `ProgramArguments`.
- No TCC permissions needed. Network listening triggers the application firewall prompt once if the firewall is on.

### Linux

- `install-agent` writes a systemd user unit at `~/.config/systemd/user/tally.service` (`Restart=always`, `RestartSec=2`) and runs `systemctl --user enable --now tally`. Logs go to the journal.
- herdr's socket path resolution is identical, under `~/.config/herdr/`.
- If avahi-daemon holds port 5353, zeroconf still advertises through `SO_REUSEPORT`.
- No root, no udev rules, no group membership.

## Failure modes

| Failure | Behavior |
| --- | --- |
| Action in flight when the link drops | Discarded. Never retried. |
| Target pane closed | `target_gone` ack. |
| Focus changed just before a tap | The action still lands on the pane the client showed, because it carries `target`. |
| herdr stopped | `herdr:false` frames; actions return `herdr_offline`. |
| Hotel Wi-Fi isolates clients | The LAN race fails fast; Tailscale wins. |
| No Tailscale and isolated Wi-Fi | Put the laptop on the phone's hotspot; Bonjour finds it. |
| Token leaked | `tally pair --revoke` drops the session and invalidates the token. Re-pair with a new QR. |
| Pad and a remote client both connected | Both receive the same frames. The pad still types through HID; the remote client acts through herdr. |

## Invariants

- The host owns every key encoding. Clients send action names, never keys.
- DRIVE actions carry an explicit agent pane target and never reach a plain shell.
- Actions are never retried. A dropped action stays dropped.
- Every frame comes from one snapshot. Events only invalidate.
- Slot n = `agents[n-1]`, never re-sorted.
- No titles, cwds or terminal output leave the host. Frames carry ids, state codes, timers and the focused space label only.
- The serial path stays read-only. Only `--remote`, `--web` and `tally act` construct the herdr `Writer`.
- Nothing is accepted before auth.

## Testing

Action table against a fake `herdr.Writer` (every action, agent-only targeting, `target_gone`, REWIND gap, NEW/RESUME cwd handling without leakage); pairing round trip; auth (bad token, late auth, rate limit, revoke drops a live session); v3 frame encoding; Bonjour advertisement in-process; `blocked_since` across snapshots.
