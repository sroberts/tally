# Tally: MacroPad Status Lights for Claude Code

Sep 30, 2026 · @Scott J Roberts

## Bottom line

Tally lights each MacroPad Fleet key with the live state of the herdr agent in that sidebar slot: amber when it is blocked on you, green when it finished and you have not looked, blue while it works. herdr already tracks every agent's state, so Tally needs no Claude Code hooks: a small Go daemon on the Mac subscribes to herdr's local socket, maps agents to sidebar slots 1 through 6, and streams a full-state frame over the MacroPad's USB CDC data port. CircuitPython firmware renders the frame on the NeoPixels and OLED, aware of which layer (Drive or Fleet) is active.

Key decisions:

- **herdr is the source of truth.** `herdr integration install claude` makes herdr report each Claude Code session as `working`, `blocked`, `done`, or `idle`. Tally reads that state; it never parses Claude Code output or installs hooks.
- **Slot = herdr sidebar position.** Fleet keys A1 to A6 send `focus_agent` 1 to 6, which follows the sidebar order. Tally computes the same order, so the key that lights is the key that jumps there.
- **Events invalidate, reads decide.** Socket events trigger a debounced `session.snapshot` read, as herdr's API docs require. Tally never replays event payloads as state.
- **Daemon owns state; device owns rendering.** The daemon sends semantic state per slot. The firmware picks colors per layer, because only the device knows the active layer.
- **Full-state frames, not deltas.** Every frame carries all six slots. A dropped line self-heals on the next frame or the 5 s heartbeat.
- **launchd agent on macOS.** The daemon runs as a per-user LaunchAgent, restarts on failure, and needs no root.

## Goals, non-goals, assumptions

Tally answers one question at a glance: which agent needs me right now?

**Goals**

1. Show per-agent state for up to 6 agents on the Fleet layer keys, updated within 250 ms of herdr's state change.
2. Show the focused agent's state on the Drive layer (permission row, ESC, BG, ENTER) so the pad tells you which keys are live.
3. Clear the "done" signal automatically when you look at that agent, using herdr's own seen tracking.
4. Survive unplug/replug, daemon restart, herdr server restart, and agents moving between tabs or workspaces without manual resync.
5. Add no visible latency or setup to Claude Code sessions.

**Non-goals (v1)**

- Answering prompts from the pad by any channel other than HID keystrokes. The pad stays a keyboard; Tally never sends input to herdr or Claude Code.
- Agents on other machines connected to herdr. v1 reads the local herdr session only.
- An error state for API failures. herdr reports no such status; a failed turn shows as `done`.
- Usage and quota display. That stays with the PyPortal project, which can read `GET /v1/state` if useful.

**Assumptions**

- The host is a Mac running herdr's default session, with `herdr integration install claude` applied.
- herdr's `ui.agent_panel_sort` stays at its default, `spaces`. The `priority` sort reorders agents by attention, which would move agents between keys as their state changes.
- No plugin replaces the Agents view with `agent.view.set`. A custom view changes `focus_agent` order, and Tally would mislight keys.
- The MacroPad runs CircuitPython 10.x with `boot.py` enabling the `usb_cdc` data port.

## Architecture

Three components, one direction of truth: herdr knows each agent's state, the daemon maps it to slots, the pad renders it.

&#91;embedded content: Tally architecture · 3 components, 2 USB channels\]

Events from herdr's socket only tell the state actor to re-read; every frame comes from one `session.snapshot`. Frames flow to the pad over the USB CDC data channel, and the pad's only reply is `hello`. Keystrokes, including the TALK key's held Right Option, reach herdr and Claude Code over the separate USB HID channel, never through the daemon.

## Session state model

Tally adds no state of its own: each slot shows herdr's effective agent status, translated to a one-letter code.

| Code | herdr status | Meaning | Fleet key rendering |
| --- | --- | --- | --- |
| `N` | `blocked` | Waiting on you: a permission prompt, a question, or an MCP elicitation | Amber, 1 Hz blink, full brightness |
| `G` | `done` | Finished a turn you have not looked at yet | Green, solid |
| `W` | `working` | Processing a turn | Blue, 2 s breathe between 15% and 35% |
| `R` | `idle` | Waiting for a prompt; you have seen the result | White, solid, 25% |
| `U` | `unknown` | herdr cannot classify the pane yet | White, solid, 10% |
| (none) | no agent | No agent in that slot | Off |

The focused agent's key renders at full scale; other keys render at 60% of the listed brightness. Percentages scale the full-brightness color before the pad's global brightness (0.3) applies, so anything much under 25% is hard to see; `R` and `U` were raised from 8% and 3% after the first hardware test, where idle keys looked off. When signals compete for the OLED summary, `N` beats `G` beats `W`.

### Where the transitions come from

herdr owns every transition, so Tally's daemon has no transition table:

- **Working, blocked, idle.** herdr's Claude integration reports these as Claude Code moves through a turn.
- **Done to idle.** herdr reports `done` for an idle agent whose result is unseen, and clears it when you focus that pane. Tally needs no focus-acknowledgement logic.
- **Interrupts.** Pressing ESC ends the turn; herdr reports `idle` once Claude Code settles. Tally needs no ESC reporting from the pad.
- **Agent appears or exits.** herdr emits `pane.agent_detected` and `pane.exited`; the slot fills or empties on the next snapshot.

Subagents run inside their parent's pane, so they never take a slot of their own.

## herdr integration

Tally needs one herdr setup step and reads everything else from herdr's socket API; Claude Code itself needs no configuration.

**One-time setup**

```bash
herdr integration install claude
herdr integration status
```

`tally doctor` checks the integration status and that `ui.agent_panel_sort` is `spaces`. It cannot check for an active Agents view: herdr only sets or clears one (`agent.view.set`, `agent.view.clear`), both writes, so doctor says so instead.

**Socket**

herdr serves newline-delimited JSON on a Unix socket. Tally resolves the path the way the herdr CLI does: `HERDR_SOCKET_PATH`, then `HERDR_SESSION`, then the default `~/.config/herdr/herdr.sock`.

**Calls Tally makes**

| Call | Purpose |
| --- | --- |
| `ping` | Liveness check in `tally status`. The daemon skips it: a failed snapshot already means herdr is offline. |
| `session.snapshot` | Authoritative read: ordered workspaces, tabs, panes, layouts, focused pane, and agent records with `agent_status`. Every frame comes from one snapshot. |
| `events.subscribe`, connection A | Global lifecycle stream: `workspace.created`, `.moved`, `.reordered`, `.closed`, `.focused`; `tab.created`, `.closed`, `.moved`, `.focused`; `pane.created`, `.closed`, `.moved`, `.exited`, `.focused`, `.agent_detected`; `layout.updated`. |
| `events.subscribe`, connection B | `pane.agent_status_changed` for each agent pane. herdr requires a `pane_id` per entry and rejects the whole request if any pane is gone, so Tally rebuilds this connection from the latest snapshot whenever the agent pane set changes. |

Every event is an invalidation signal. The daemon waits 50 ms for more events, then issues one `session.snapshot` and renders from it. It never applies an event payload to its cache, because herdr guarantees no ordering between events and snapshots. It never even decodes one: `pane.agent_status_changed` carries the pane title. A subscription's acknowledgement (`subscription_started`) also invalidates, so every (re)subscribe is followed by a fresh snapshot.

**Recovery**

- `events_lost` on either connection: reopen it at once, wait for `subscription_started`, then take a fresh snapshot.
- Any other subscription failure (herdr rejected a gone pane, the connection dropped): invalidate, then retry after 2 s, or sooner for connection B when a new pane set arrives.
- Socket gone (herdr server stopped): mark every slot empty, show `HERDR OFFLINE` on the OLED, retry every 2 s.
- A 2 s safety poll takes a snapshot even with no events, which bounds any missed event to 2 s of staleness.

## Daemon (`tally`)

The daemon is a single static Go binary with one goroutine owning all state; every other goroutine sends it messages.

### Stack

- Go 1.23+, standard library `net`, `net/http`, `encoding/json`.
- `go.bug.st/serial` and its `enumerator` package for USB CDC discovery.
- No database and no herdr client library. State is the latest snapshot, held in memory.

### Goroutines

1. **Lifecycle subscriber.** Holds herdr connection A and turns every pushed event into an `Invalidate` message.
2. **Status subscriber.** Holds connection B, subscribed to `pane.agent_status_changed` for the current agent panes. Rebuilds itself when the actor sends a new pane set (compared sorted, so a reorder alone keeps the connection).
3. **State actor.** Debounces `Invalidate` for 50 ms from the first one, takes a `session.snapshot` on its own short-lived connection, computes slots, and emits a frame when the result differs. Invalidations that arrive during a snapshot queue one more refresh.
4. **Serial manager.** Discovers, opens, and reconnects the device; writes each new frame and resends the current one on the heartbeat and on `hello`; reads `hello` lines. The actor hands it frames through a one-slot mailbox, so a stuck port never stalls the actor.
5. **HTTP server** (not built yet). Serves `GET /v1/state` and `GET /healthz` on `127.0.0.1:47823` for `tally status` and the PyPortal.

Milestone 3 built 1 to 4 (`internal/daemon`, `device.Link`) and `tally serve` with flags only; the HTTP server and the config file are still to come.

### Slot computation

Slot n is the snapshot's `agents[n-1]`. Under the default `spaces` sort, herdr draws the Agent sidebar straight from that list, and `focus_agent` indexes it. The server builds the list by walking workspaces, then tabs, then each tab's panes in layout order, and Tally keeps that order rather than recomputing it (herdr v0.9.3: `collect_agent_infos` in `src/app/agents.rs`, `ordered_agent_pane_ids` in `src/client/shell/agent_sidebar.rs`). The first 6 agents become slots 1 to 6; the rest count toward `overflow`.

The focused slot is the agent whose pane matches the snapshot's focused pane id, or none when the focus is a plain shell.

herdr answers one request per connection and then closes it, so every `ping` and `session.snapshot` dials afresh. Only `events.subscribe` holds a connection open.

### Serial discovery

1. Enumerate ports with USB VID `0x239A` (Adafruit). On macOS, open the `/dev/cu.usbmodem*` device, never `/dev/tty.usbmodem*`, which blocks on carrier detect.
2. The MacroPad exposes two ports, the REPL console and the data port. Send `{"t":"ping"}` to each and keep the one that answers `hello` within 1 s; the console only echoes REPL output.
3. On any read or write error, close, back off 2 s, rediscover. Never crash on a missing device.

`serial_port` in config overrides discovery with an explicit path.

### Frame emission

- Emit after each snapshot whose slots, focus, or overflow differ from the last frame.
- Heartbeat: the serial manager resends the current frame every 5 s even when nothing changed.
- On device connect or `hello`, send the current frame immediately.

### Configuration

`~/.config/tally/config.toml`, every key optional:

```toml
herdr_socket = "auto"    # HERDR_SOCKET_PATH, HERDR_SESSION, then ~/.config/herdr/herdr.sock
slots        = 6
debounce_ms  = 50
safety_poll_ms = 2000
serial_port  = "auto"
listen       = "127.0.0.1:47823"
log_level    = "info"
```

### CLI

| Command | Purpose |
| --- | --- |
| `tally serve` | Run the daemon (launchd calls this). |
| `tally status` | Pretty-print `/v1/state`: slot grid, herdr statuses, device link. |
| `tally doctor` | Check herdr socket, Claude integration, `ui.agent_panel_sort`, the LaunchAgent, and the pad (handshake, or port held by the agent). |
| `tally install-agent`, `tally uninstall-agent` | Install or remove the LaunchAgent. |
| `tally simulate <scenario>` | Send scripted frames (`all-states`, `blocked-unfocused`, `offline`) straight to the device, no herdr needed. |
| `tally frame '<json>'` | Send one raw frame to the device. |

### Packaging and service

- Build: `go build -o ~/.local/bin/tally ./cmd/tally`, or `go install`.
- Service: `tally install-agent [--port PATH] [--slots N]` writes `~/Library/LaunchAgents/local.tally.plist` (`RunAtLoad`, `KeepAlive`, `ProcessType` Interactive, the running binary's resolved path, and `HERDR_SOCKET_PATH`/`HERDR_SESSION` from the installing shell), boots out any loaded copy, loads it with `launchctl bootstrap gui/$(id -u)`, then `launchctl kickstart`s it. `tally uninstall-agent` boots it out and removes the plist.
- Kickstart is needed: on this macOS, launchd held a fresh bootstrap at `pended nondemand spawn = speculative` (never started) and, after the daemon was killed, held the `KeepAlive` restart at `inefficient` for minutes, on AC power, for any job (`/bin/sleep` too). So `KeepAlive` cannot be counted on, and `tally doctor` reports a held-back start with the `launchctl kickstart` command that clears it. The daemon itself never exits on herdr or device errors, so a restart is needed only after a crash or a kill. Whether `RunAtLoad` starts it at login is still to be confirmed.
- Logs: stdout and stderr go to `~/Library/Logs/tally.log` (no rotation; a line per frame change).
- macOS grants USB serial access to the logged-in user, so no driver, entitlement, or root step is needed.

## Serial protocol

Newline-delimited JSON over the CircuitPython USB CDC data port, protocol version 2; every host frame is a complete snapshot, so the device never reconstructs history.

### Framing

- UTF-8, one JSON object per line, terminated by `\n`.
- Maximum 512 bytes per line. The receiver discards an over-length line and resynchronizes at the next `\n`.
- Every message has a `t` (type) field. Receivers ignore unknown types and unknown fields.
- USB CDC ignores baud rate; the host opens at 115200 for tooling compatibility.

### Host to device

**`state`**, sent on change and every 5 s:

```json
{"t":"state","v":2,"seq":1042,
 "slots":["W","N",null,"G","R",null],
 "focus":2,"fstate":"N",
 "overflow":{"N":1},"herdr":true}
```

| Field | Type | Meaning |
| --- | --- | --- |
| `v` | int | Protocol version. Firmware shows `UPDATE FW` and ignores frames above its supported version. |
| `seq` | uint32 | Increments per frame; resets on daemon restart. Debug display only. |
| `slots` | array, length = `slots` config | State code per sidebar slot 1..N (`N`, `G`, `W`, `R`, `U`), `null` when empty. |
| `focus` | int or null | Focused slot, 1-based. `0` when the focused agent sits beyond the slots; `null` when the focus is not an agent. |
| `fstate` | string or null | State of the focused agent, present even when `focus` is `0`. Drives Drive-layer key lighting. |
| `overflow` | object | Count by state for agents beyond the slot count. |
| `herdr` | bool | `false` when the daemon cannot reach herdr's socket. |
| `space` | string, optional | Name of the focused agent's space (herdr workspace `label`), printable ASCII, at most 10 characters. Omitted when nothing is focused or the name is empty. Older firmware ignores it. |

**`ping`**: `{"t":"ping"}`, sent during discovery. The device answers with `hello`.

### Device to host

| Message | When | Example |
| --- | --- | --- |
| `hello` | Boot, and in reply to `ping` | `{"t":"hello","v":2,"fw":"0.2.0"}` |

The device reports nothing else. herdr already sees interrupts and focus changes, so v1's `key` and `layer` messages are gone.

### Liveness

- The device marks the link **stale** after 15 s without a `state` frame (three missed heartbeats): Fleet keys drop to their static color at 10%, the OLED shows `TALLY OFFLINE`. The keymap keeps working.
- A frame with `"herdr":false` shows `HERDR OFFLINE` with the same dimmed keys.
- The first good frame restores full rendering with no handshake.

## Firmware (MacroPad RP2040, CircuitPython)

The firmware extends the keypad `code.py` that MacroPad Layout Studio generates: it keeps serving HID keys first and lighting second, so a slow or absent host never delays a keystroke.

### Files

| File | Responsibility |
| --- | --- |
| `boot.py` | `usb_cdc.enable(console=True, data=True)`. Already installed; takes effect after a hard reset. |
| `code.py` | Generated by the studio: keymap, `Hold` keys, layer state, main loop. Tally adds three calls to the loop. |
| `tally_link.py` | Non-blocking line reader on `usb_cdc.data`, JSON parse, `hello` replies, stale tracking. |
| `render.py` | Pure function: (layer, frame, held keys, time) to 12 RGB values. |
| `oled.py` | displayio labels for the status lines; redraws only when text changes. |

Dependencies: `adafruit_macropad` and its bundled requirements, installed with `circup install adafruit_macropad`.

### Main loop (target 100 Hz)

1. Drain `macropad.keys.events`; run each key's action. `Hold` keys press on key-down and release on key-up.
2. Read encoder delta and switch; rotate runs the layer's encoder action, press toggles layer and calls `kbd.release_all()`.
3. `tally_link.poll()`: read `usb_cdc.data.in_waiting` bytes without blocking, split on `\n`, `json.loads` inside `try`, keep the last valid `state` frame.
4. Every 33 ms (30 fps), call `render.py` and `pixels.show()` once. `pixels.auto_write = False`; global brightness capped at 0.4.
5. Update OLED text if changed.

Without a frame (Tally not installed), `render.py` falls back to the static per-key colors the studio defines, so the pad works exactly as it does today.

### Key indices

MacroPad keys number 0 through 11, row-major from the top-left: row 1 is keys 0-2, row 4 is keys 9-11. Fleet slots 1-6 map to keys 0-5.

The key numbers in the tables below describe the default layout. `render.py` matches keys by OLED label (`A1`-`A6`, `ATTN`, `ENTER`, `ESC`, ...), not position, so a layout rearranged in MacroPad Layout Studio keeps its lighting.

### Lighting by layer

**Fleet layer**

| Key | Binding | Lighting |
| --- | --- | --- |
| 0-5 | A1-A6, focus agent 1-6 | Slot state per the state table |
| 6 | ATTN | Amber 1 Hz blink when any agent is `N`, including overflow; else amber 10% |
| 7 | NEW | Blue 10% |
| 8 | RESUME | Blue 10% |
| 9 | ZOOM | Blue 10% |
| 10 | LAST | Blue 10% |
| 11 | ENTER | Green 40% when `fstate` is `R` or `G`, else off |

**Drive layer** (lights show which keys matter for the focused agent)

| Key | Binding | Lighting |
| --- | --- | --- |
| 0 | ESC | Red 30% when `fstate` is `W` or `N`, else off |
| 1 | MODE | Blue 10% |
| 2 | REWIND | Blue 10% |
| 3-5 | YES / ALWAYS / NO | Amber 1 Hz blink when `fstate` is `N`, else off |
| 6 | TAB | White 8% |
| 7 | LOG | White 8% |
| 8 | COMPACT | White 8% |
| 9 | ENTER | Green 40% when `fstate` is `R` or `G`, else off |
| 10 | TALK, holds Right Option | Purple 15%; purple 100% while held, with or without Tally |
| 11 | BG | Blue 30% when `fstate` is `W`, else off |

Drive lighting needs a live link and herdr; otherwise Drive keeps the studio's static colors.

**Cross-agent alert (both layers).** When the number of blocked agents (slots plus overflow) rises and the newcomer is not the focused agent, the whole pad flashes amber twice (2 x 100 ms on, 100 ms off), then returns to the layer's lighting. The alert is edge-triggered: one flash per transition, never repeating while the state holds. It never fires while TALK is held, so dictation is not interrupted; pressing TALK cancels a flash in progress. Counting rather than tracking slot positions means a reorder or a focus change never flashes, and the first frame after a (re)connect sets the baseline without flashing.

### OLED (128 x 64, `terminalio` font, 4 lines of 21 characters)

The keycaps carry the labels, so once Tally is running the OLED shows context instead:

```
FLEET A2 vibe-macro *
A2 NEEDS APPROVAL 5m
A5 WAITING 3m  DONE 4
1W 2N 3- 4G 5N 6-  +1
```

1. Layer, focused slot (`+` when beyond the slots), the focused agent's space name, link indicator (`*` live, `x` stale, `h` herdr offline).
2. Focused state in words (`READY`, `WORKING`, `NEEDS APPROVAL` with how long, `DONE`, `UNKNOWN`), `NO AGENT FOCUSED`, or `TALLY OFFLINE`, `HERDR OFFLINE`, `UPDATE FW`.
3. Other agents waiting on you, longest first, with how long (`A5 WAITING 3m`, or `WAIT A3 4m A1 1m`); blocked agents beyond the slots (`+1 WAIT`); slots done but unseen (`DONE 4`). Whole items that do not fit are dropped.
4. Every slot at a glance: slot number then state code (the state table's `N`, `G`, `W`, `R`, `U`), `-` when empty, then `+N` for agents beyond the slots. So `5N` means A5 needs you and `6G` means A6 finished unseen.

Wait times are measured on the pad from the first frame that shows a slot blocked, so they restart if the pad reloads, and they follow slot positions. Until Tally sends its first frame the OLED keeps the studio's label grid. The text is rebuilt only when the frame, link status or layer changes, or once a second for the timers, and each line redraws only when its text changes.

## Security, privacy, failure modes

Tally only reads herdr's socket and never writes to it, so the worst a Tally bug can do is light the wrong key.

### Security and privacy

- **Read-only herdr access.** Tally calls `ping`, `session.snapshot`, and `events.subscribe` only. It never calls `pane.send_*`, `agent.prompt`, `agent.focus`, or any write method. Code review enforces this with a method allowlist in `internal/herdr`.
- **Data minimization.** The snapshot decoder keeps ids, order, focus, `agent_status`, and each space's name (shown on the OLED, and in the daemon's frame log). It drops tab and pane titles, `cwd`, `tokens`, `agent_session`, and every other field before they reach memory or logs.
- **Loopback only.** The HTTP endpoint binds `127.0.0.1:47823` and serves read-only JSON. A local process can read slot states; it cannot change anything.
- **No serial command surface.** The firmware accepts `state` and `ping` only. Nothing received over serial can trigger HID output.
- **Logs.** `~/Library/Logs/tally.log`: start, device connect and disconnect, herdr offline and back, subscription errors, and each new frame (state codes and the focused space's name). No pane or tab title, cwd, or terminal output.

### Failure modes

| Failure | Behavior |
| --- | --- |
| Daemon crashed | launchd `KeepAlive` restarts it; the pad dims after 15 s and recovers on the first frame. |
| herdr server stopped | Daemon sends `"herdr":false`; OLED shows `HERDR OFFLINE`; daemon retries every 2 s. |
| `events_lost` | Daemon resubscribes, then takes a fresh snapshot. |
| A subscribed pane closes mid-request | herdr rejects connection B's whole request; daemon rebuilds it from a new snapshot. |
| Missed event | The 2 s safety poll corrects the frame. |
| MacroPad unplugged | Daemon keeps state and retries discovery every 2 s; frames resume on replug. |
| Agents reordered or moved between tabs | Next snapshot recomputes slots; keys follow the sidebar. |
| `ui.agent_panel_sort` set to `priority`, or a plugin Agents view active | Keys and lights can disagree. `tally doctor` flags both. |
| More than 6 agents | Agents beyond slot 6 count in `overflow` on the OLED and still trigger ATTN. |
| TALK held during a layer switch | Firmware releases all keys, so Right Option never sticks. |

## Repo, testing, acceptance, milestones

One repo holds the daemon, the firmware, and the studio-generated keymap; each milestone lands as its own PR off `main`.

### Layout

```
tally/
  cmd/tally/            main.go, CLI wiring
  internal/herdr/       socket client, snapshot decoder, subscriptions, method allowlist
  internal/slots/       slot, focus and overflow computation, frames
  internal/device/      discovery, handshake, frame writer, reconnecting link
  internal/daemon/      subscribers and state actor (tally serve)
  internal/config/
  firmware/             boot.py, code.py (from the studio), tally_link.py, render.py, oled.py
  testdata/snapshots/   captured session.snapshot responses (titles and cwds redacted)
  launchd/              local.tally.plist template
```

### Testing

- **Slot order.** Table-driven Go tests over `testdata/snapshots/`: multiple workspaces, split panes, an agent in a later tab, more than 6 agents, focus on a plain shell.
- **Decoder.** Golden tests asserting no dropped field (titles, cwds, tokens) reaches the struct.
- **herdr client.** A fake Unix-socket server replays subscription acks, events, `events_lost`, and rejected pane subscriptions.
- **Device link.** A pty pair stands in for the serial port; tests assert framing, the 512-byte cap, heartbeat cadence, and reconnect.
- **Firmware render.** `render.py` stays free of hardware imports so CPython runs its tests on the host, including the TALK-held case.
- **End to end.** `tally simulate all-states` against real hardware, checked by eye against the expected key colors.

### Acceptance criteria

- [ ] An agent in sidebar slot 3 hitting a permission prompt turns key 3 amber within 250 ms of herdr reporting `blocked`.
- [ ] Approving with YES returns key 3 to blue breathe once herdr reports `working`.
- [ ] A turn finishing in an unfocused agent shows green; focusing it with its A key turns it dim white.
- [ ] Pressing A1 to A6 always focuses the agent whose key was lit, with four agents across two workspaces.
- [ ] Stopping and restarting the herdr server shows `HERDR OFFLINE`, then recovers with no command.
- [ ] Unplugging and replugging the pad restores correct colors within 5 s with no command.
- [ ] Holding TALK keeps Right Option down for the full hold, and the cross-agent alert never flashes during it.
- [ ] `tally doctor` passes on a fresh LaunchAgent install.
- [ ] No pane title, cwd, or terminal output appears in the log at any level.

### Milestones

1. **Device link.** Firmware `tally_link.py` and `render.py` wired into the studio's `code.py`; `tally frame` sends hand-written frames. Proves the CDC data port and lighting.
2. **herdr reader.** Socket client, snapshot decoder, slot computation, `tally status`. Verifies slot order against the herdr sidebar.
3. **Live frames.** Subscriptions, debounce, safety poll, recovery paths.
4. **Drive-layer integration.** `fstate` lighting, cross-agent alert, OLED status lines.
5. **Packaging.** `tally install-agent`, `tally doctor`; acceptance pass on the Mac. The HTTP endpoint and the config file are not built; flags cover the configuration so far.

### Open questions

- Should the PyPortal broker consume `GET /v1/state`, or stay independent?
- Should MacroPad Layout Studio generate the Tally firmware hooks too, so one export covers keymap and lighting?
