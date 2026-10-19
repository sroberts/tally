# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

tally is the Go host daemon shared by its clients: the hardware MacroPad (repo `vibe-macropad`, serial protocol v2, the pad types through USB HID) and remote clients: paired phones over TLS WebSocket protocol v3 and the browser pad, whose actions tally executes against herdr. All clients run off the same actor and snapshot logic. Read `docs/design/tally-macropad.md` before daemon work, `docs/design/remote.md` before remote/actions work and `docs/design/browser-pad.md` before web work.

Workflow, commands, testing and the rules every change keeps are in `docs/developer.md`, imported here:

@docs/developer.md

The README is for users; keep development detail in `docs/developer.md`.

## Packages

- `internal/herdr` is a read-only socket client: an allowlist refuses any method but `ping`, `session.snapshot` and `events.subscribe`, and the snapshot structs declare only ids, order, focus and status, so titles and cwds are dropped while decoding. herdr answers **one request per connection**, so each call dials afresh. Only `tally serve --remote`/`--web` and `tally act` construct the write-capable `Writer`; the read-only `Client` and its allowlist stay untouched.
- `internal/slots` maps agents to slots. Slot n is the snapshot's `agents[n-1]`: herdr's sidebar and `focus_agent` use that list's order as-is, so never re-sort it. The frame's optional `space` field is the focused agent's herdr workspace `label`; the fixture's space labels are `space-label-N` while tab labels stay `SECRET-` markers.
- `internal/daemon` is `tally serve`: a lifecycle subscription and a per-pane `pane.agent_status_changed` subscription only invalidate. One actor goroutine debounces 50 ms, takes a snapshot, and emits a frame when it differs, with a 2 s safety poll. Event payloads are never decoded (they carry pane titles). `herdr.Subscribe` calls `notify` on the ack too, so every resubscribe reconciles.
- `internal/device` does pad port discovery (Adafruit VID `239A`, `/dev/cu.*` on macOS, ping each port and keep the one that answers `hello`) and v2 frame encoding. `device.Link` keeps the pad connected: it rediscovers after any error, resends the current frame on connect, on `hello`, and every 5 s. `Send` never blocks.
- `internal/launchd` writes and loads the `local.tally` LaunchAgent and parses `launchctl print`; `internal/doctor` holds `tally doctor`'s pure checks. launchd on this macOS holds back `RunAtLoad`/`KeepAlive` starts (`pended nondemand spawn`), so install always ends with `launchctl kickstart`.
- `internal/actions` is the remote clients' action table: name → herdr calls, the only place key encodings live. DRIVE actions need a target pane and are checked against a fresh snapshot, so they only reach agent panes; FLEET actions move focus, zoom or open a tab. `Recorder` is a Writer that writes nothing (`tally act --dry-run`, doctor, tests). NEW/RESUME read the cwd only inside `open`, and errors from `tab.create` drop herdr's message in case it quotes the cwd.
- `herdr.Writer` has its own write allowlist (`pane.send_keys`, `send_text`, `send_input`, `focus`, `zoom`, `get`, `tab.create`). herdr has no `pane.run` socket method: `Run` sends `pane.send_input` with one of two fixed `herdr.Command`s (`claude`, `claude --resume`), which no other package can construct.
- `slots.Tracker` (owned by the actor) keeps blocked-since times and the previous focused pane across snapshots. `daemon.OnUpdate` hands every snapshot attempt to the remote listener; `State.FrameV3` builds the v3 frame. The v2 `Frame` must stay byte-identical for the pad's firmware.
- `internal/pairing` makes the ECDSA P-256 cert once and keeps `~/.config/tally/{cert.pem,key.pem,remote.json}` (0600; token hashes only). `internal/remote` is the TLS 1.3 WebSocket server (auth within 2 s, 5 failures/IP/minute, revocation polled from remote.json every second, Bonjour `_tally._tcp`). The wire format is `docs/protocol-v3.md`; change every client with it.
- `internal/web` is the browser pad: an HTTPS server (`--web`, :47825) serving `static/` (embedded; no build step) to browsers signed in with a password. `web.json` keeps an argon2id hash and each browser's cookie hash (`__Host-tally`, Secure, HttpOnly, SameSite=Strict). `/ws` hands the socket to `remote.Server.Attach`, so browsers share the phones' sessions, action table and allowlist; the phones' revocation watch skips them (empty token hash) and `web` closes them itself. Certificates come from `tailscale cert` (`web.Tailscale`, refreshed daily) or tally's own (`--web-cert self`). `static/lighting.js` ports the hardware pad's lighting rules (vibe-macropad's render.py) and `lighting_test.mjs` holds the same cases as its tests, so change lighting in every client together.
- `internal/systemd` is the Linux counterpart of `internal/launchd` (a user unit, `Restart=always`).
- `cmd/tally` is the CLI.

## Notes for Claude

In this sandbox git can't read `~/.gitconfig`; prefix git (and `go build`, which stamps VCS info) with `GIT_CONFIG_GLOBAL=/dev/null`.

Never send keys to live herdr panes from tests or while developing: the user's real agents run there. Test writes against a fake writer, or a scratch tab you create and close.

Push, open PRs or merge only when the user asks; merges are merge commits.
