# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

tally is the Go host daemon shared by two clients: the hardware MacroPad (repo `vibe-macropad`, serial protocol v2, the pad types through USB HID) and the iPhone soft pad (repo `vibepad`, TLS WebSocket protocol v3, tally executes its actions against herdr). Both clients run off the same actor and snapshot logic. Read `docs/design/tally-macropad.md` before daemon work and `docs/design/softpad.md` before remote/actions work.

Work follows GitHub Flow: branch, PR, merge commit on GitHub. Local hooks block commits and pushes on `main`.

## Packages

- `internal/herdr` is a read-only socket client: an allowlist refuses any method but `ping`, `session.snapshot` and `events.subscribe`, and the snapshot structs declare only ids, order, focus and status, so titles and cwds are dropped while decoding. herdr answers **one request per connection**, so each call dials afresh. Only `tally serve --remote`/`--web` and `tally act` construct the write-capable `Writer`; the read-only `Client` and its allowlist stay untouched.
- `internal/slots` maps agents to slots. Slot n is the snapshot's `agents[n-1]`: herdr's sidebar and `focus_agent` use that list's order as-is, so never re-sort it. The frame's optional `space` field is the focused agent's herdr workspace `label`; the fixture's space labels are `space-label-N` while tab labels stay `SECRET-` markers.
- `internal/daemon` is `tally serve`: a lifecycle subscription and a per-pane `pane.agent_status_changed` subscription only invalidate. One actor goroutine debounces 50 ms, takes a snapshot, and emits a frame when it differs, with a 2 s safety poll. Event payloads are never decoded (they carry pane titles). `herdr.Subscribe` calls `notify` on the ack too, so every resubscribe reconciles.
- `internal/device` does pad port discovery (Adafruit VID `239A`, `/dev/cu.*` on macOS, ping each port and keep the one that answers `hello`) and v2 frame encoding. `device.Link` keeps the pad connected: it rediscovers after any error, resends the current frame on connect, on `hello`, and every 5 s. `Send` never blocks.
- `internal/launchd` writes and loads the `local.tally` LaunchAgent and parses `launchctl print`; `internal/doctor` holds `tally doctor`'s pure checks. launchd on this macOS holds back `RunAtLoad`/`KeepAlive` starts (`pended nondemand spawn`), so install always ends with `launchctl kickstart`.
- `internal/actions` is the soft pad's action table: name → herdr calls, the only place key encodings live. DRIVE actions need a target pane and are checked against a fresh snapshot, so they only reach agent panes; FLEET actions move focus, zoom or open a tab. `Recorder` is a Writer that writes nothing (`tally act --dry-run`, doctor, tests). NEW/RESUME read the cwd only inside `open`, and errors from `tab.create` drop herdr's message in case it quotes the cwd.
- `herdr.Writer` has its own write allowlist (`pane.send_keys`, `send_text`, `send_input`, `focus`, `zoom`, `get`, `tab.create`). herdr has no `pane.run` socket method: `Run` sends `pane.send_input` with one of two fixed `herdr.Command`s (`claude`, `claude --resume`), which no other package can construct.
- `slots.Tracker` (owned by the actor) keeps blocked-since times and the previous focused pane across snapshots. `daemon.OnUpdate` hands every snapshot attempt to the remote listener; `State.FrameV3` builds the v3 frame. The v2 `Frame` must stay byte-identical for the pad's firmware.
- `internal/pairing` makes the ECDSA P-256 cert once and keeps `~/.config/tally/{cert.pem,key.pem,remote.json}` (0600; token hashes only). `internal/remote` is the TLS 1.3 WebSocket server (auth within 2 s, 5 failures/IP/minute, revocation polled from remote.json every second, Bonjour `_tally._tcp`). The wire format is `docs/protocol-v3.md`; keep it in step with the iOS app in `vibepad`.
- `internal/web` is the browser pad: an HTTPS server (`--web`, :47825) serving `static/` (embedded; no build step) to browsers signed in with a password. `web.json` keeps an argon2id hash and each browser's cookie hash (`__Host-tally`, Secure, HttpOnly, SameSite=Strict). `/ws` hands the socket to `remote.Server.Attach`, so browsers share the phones' sessions, action table and allowlist; the phones' revocation watch skips them (empty token hash) and `web` closes them itself. Certificates come from `tailscale cert` (`web.Tailscale`, refreshed daily) or tally's own (`--web-cert self`). `static/lighting.js` ports vibepad's Lighting.swift and Readout.swift; `lighting_test.mjs` holds the same cases as LightingTests.swift, so change both apps' lighting together.
- `internal/systemd` is the Linux counterpart of `internal/launchd` (a user unit, `Restart=always`).
- `cmd/tally` is the CLI.

## Invariants

- The serial path stays read-only. Only `tally serve --remote`/`--web` and `tally act` construct the herdr writer.
- Actions are never retried. Nothing is accepted from a phone before auth, or from a browser before sign-in.
- No titles, cwds or terminal output leave the host or reach a log line.
- The v2 serial frame stays byte-compatible with the pad firmware in `vibe-macropad`; protocol changes for the phone go in v3 only.

## Commands

```bash
go test ./...
go test ./internal/device -run TestHandshake
go build -o ~/.local/bin/tally ./cmd/tally
tally status                         # herdr's agents in slot order
tally serve                          # Ctrl-C stops
tally install-agent                  # LaunchAgent (log: ~/Library/Logs/tally.log)
tally doctor
tally status --json | tally frame -  # light the pad from live herdr state, once
tally act submit --dry-run           # print an action's herdr calls without making them
tally pair --json                    # pair a phone; --json also prints the payload for the Simulator
tally serve --remote --no-device --listen 127.0.0.1:47999   # a soft pad listener beside the real one
tally serve --web --web-cert self --no-device --web-listen 127.0.0.1:47899   # a browser pad beside the real one
```

`go test ./internal/remote` runs the listener in-process against a fake writer; the Bonjour test needs multicast (`-short` skips it). To try a live listener without touching your pairing, set `HOME` to a scratch directory for both `tally pair` and `tally serve` (and `tally web --set-password --password-stdin`), with `HERDR_SOCKET_PATH` pointing at the real socket. `go test ./internal/web` runs the browser pad in-process and runs `lighting_test.mjs` when node is installed.

`testdata/snapshots/` holds captured `session.snapshot` responses with every private string replaced by a `SECRET-` marker; the decoder test fails if one survives.

In this sandbox git can't read `~/.gitconfig`; prefix git (and `go build`, which stamps VCS info) with `GIT_CONFIG_GLOBAL=/dev/null`.

Never send keys to live herdr panes from tests or while developing: the user's real agents run there. Test writes against a fake writer, or a scratch tab you create and close.
