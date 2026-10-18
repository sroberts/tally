# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

tally is the Go host daemon shared by two clients: the hardware MacroPad (repo `vibe-macropad`, serial protocol v2, the pad types through USB HID) and the iPhone soft pad (repo `vibepad`, TLS WebSocket protocol v3, tally executes its actions against herdr). Both clients run off the same actor and snapshot logic. Read `docs/design/tally-macropad.md` before daemon work and `docs/design/softpad.md` before remote/actions work.

Work follows GitHub Flow: branch, PR, merge commit on GitHub. Local hooks block commits and pushes on `main`.

## Packages

- `internal/herdr` is a read-only socket client: an allowlist refuses any method but `ping`, `session.snapshot` and `events.subscribe`, and the snapshot structs declare only ids, order, focus and status, so titles and cwds are dropped while decoding. herdr answers **one request per connection**, so each call dials afresh. Only `tally serve --remote` may construct a write-capable client; the read-only `Client` and its allowlist stay untouched.
- `internal/slots` maps agents to slots. Slot n is the snapshot's `agents[n-1]`: herdr's sidebar and `focus_agent` use that list's order as-is, so never re-sort it. The frame's optional `space` field is the focused agent's herdr workspace `label`; the fixture's space labels are `space-label-N` while tab labels stay `SECRET-` markers.
- `internal/daemon` is `tally serve`: a lifecycle subscription and a per-pane `pane.agent_status_changed` subscription only invalidate. One actor goroutine debounces 50 ms, takes a snapshot, and emits a frame when it differs, with a 2 s safety poll. Event payloads are never decoded (they carry pane titles). `herdr.Subscribe` calls `notify` on the ack too, so every resubscribe reconciles.
- `internal/device` does pad port discovery (Adafruit VID `239A`, `/dev/cu.*` on macOS, ping each port and keep the one that answers `hello`) and v2 frame encoding. `device.Link` keeps the pad connected: it rediscovers after any error, resends the current frame on connect, on `hello`, and every 5 s. `Send` never blocks.
- `internal/launchd` writes and loads the `local.tally` LaunchAgent and parses `launchctl print`; `internal/doctor` holds `tally doctor`'s pure checks. launchd on this macOS holds back `RunAtLoad`/`KeepAlive` starts (`pended nondemand spawn`), so install always ends with `launchctl kickstart`.
- `cmd/tally` is the CLI.

## Invariants

- The serial path stays read-only. Only `--remote` constructs the herdr writer.
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
```

`testdata/snapshots/` holds captured `session.snapshot` responses with every private string replaced by a `SECRET-` marker; the decoder test fails if one survives.

In this sandbox git can't read `~/.gitconfig`; prefix git (and `go build`, which stamps VCS info) with `GIT_CONFIG_GLOBAL=/dev/null`.

Never send keys to live herdr panes from tests or while developing: the user's real agents run there. Test writes against a fake writer, or a scratch tab you create and close.
