# tally on herdr-core

The Herdr desktop app doesn't run the herdr TUI's server. It runs on herdr-core (the `hc` program), which has its own protocol, its own socket and its own agents; the two don't share anything. tally reads either one, and herdr-core is where it's heading. `--herdr auto`, the default everywhere, uses herdr-core when it answers and the Herdr app has laid out its sidebar. Otherwise it uses a herdr TUI that answers, then a bare herdr-core, and with neither answering it falls back to the TUI. A bare herdr-core is `hc` running without the app ever having opened, which has no agents to show. auto decides once per process, so `tally serve` waits up to a minute at start while neither answers, in case the Herdr app is still starting at login.

Both backends produce the same `herdr.Snapshot` and drive the same `actions.Writer`, so slots, frames, lighting, the remote listener, the browser pad, the v2 serial frame and protocol v3 don't know which one they're talking to.

## Two sockets

| | Socket | Speaks | Used for |
| --- | --- | --- | --- |
| herdr-core | `HC_SOCKET`, else `<session>.sock` (`HC_SESSION`, default `default`) under `$XDG_RUNTIME_DIR/herdr-core/`, `/run/user/<uid>/herdr-core/`, or `~/.local/state/herdr-core/<session>/` | JSON-RPC 2.0, one message per line | agents, status, events, keys, text |
| Herdr window | `$HERDR_APP_HOME/herdr-app.sock`, default `~/.config/herdr-app/` | one JSON line each way, the herdr-app CLI's format | focus, zoom, new tabs |

`tally install-agent` carries `HC_SOCKET`, `HC_SESSION` and `HERDR_APP_HOME` into the background agent, alongside the TUI's `HERDR_SOCKET_PATH` and `HERDR_SESSION`. On macOS it also carries `XDG_RUNTIME_DIR`, which launchd doesn't set but herdr-core's socket path can use; systemd gives a user unit its own.

## Reading

`herdrcore.Client` may call only `hello`, `terminal.list`, `doc.get` and `events.subscribe`. `Ping` checks that `hello` reports protocol generation 1.

`Snapshot` takes `terminal.list` and the app's layout document (`doc.get "herdr-app"`):

- **Agents.** An agent is a live terminal on this machine that reports an OSC 7501 status. Shells that never report one don't count, and neither do finished terminals or terminals on connected machines (`machine/t7`).
- **Order.** Slot n is the nth agent in the app's sidebar order: spaces, then tabs, then each tab's layout tree (`row`/`column` children in order, `block` content naming a terminal). A terminal labeled `herdr.space` for a known space but not placed yet goes after the placed ones. Before the app first runs, the document is null and there are no agents.
- **Status.** The app's own rule: `blocked` or `error` is blocked; `working` is working; otherwise an agent is done when `turn > herdr.seen` (a turn nobody has looked at), else idle. A missing `herdr.seen` label means nothing is unread. There is no unknown.
- **Space label.** The space's `name`, which fills the frame's optional `space` field.
- **Focus.** herdr-core has none. With `Client.App` set, `Snapshot` asks the window for `{"status":true}`, whose `focused_pane` is a layout node id, and maps it to that block's terminal. The status read gives up after 500 ms, so a hung window can't stall the lights or an action. A closed window means no focus, not an error.

Only ids, states, turns and the `herdr.space`, `herdr.tab` and `herdr.seen` labels are declared. Cwd, argv, title, screen, terminal names, other labels, space folders, tab names and the status message are dropped while decoding; `testdata/core/` holds scrubbed captures with `SECRET-` canaries that the decoder test checks.

## Events

herdr-core has a single `events.subscribe` stream for every terminal and document change, so `daemon.NewStream` holds one subscription where the TUI needs a lifecycle subscription plus one per agent pane. `SubscribeAll` notifies on the answer, which is a snapshot of every terminal, screens included, that it reads past with the same 45 s it allows any line. It also notifies on every notification that may change a frame: not `heartbeat`, and not `title`, `size` or `keep` events, since each snapshot reads every screen. Nothing past the method name, event kind and any error is decoded, since events carry titles and labels. An `"error": null` field isn't an error. A `missed` notice is followed by a fresh snapshot on the same stream and needs only a notify. 45 s without a line (herdr-core sends a heartbeat every 15 s) ends the subscription, and the daemon resubscribes.

The window's focus changes raise no core event, so they reach the pads within the daemon's 2 s safety poll.

## Writing

`herdrcore.Writer` exists only while `tally serve --remote/--web` or `tally act` runs.

| Action | herdr TUI | herdr-core and the window |
| --- | --- | --- |
| DRIVE keys | `pane.send_keys` | `terminal.keys` (same key names) |
| compact | `pane.send_text` + `enter` | `terminal.send` (paste and Enter in one call, through `actions.Submitter`) |
| insert_text | `pane.send_text` | refused (below) |
| focus_slot, next, prev, attention, last | `pane.focus` | window `agent.focus {terminal}` |
| zoom | `pane.zoom` | window `pane.zoom {pane}`: the window's focused pane when it shows the terminal, else the first pane that does in layout order |
| new_claude, resume_claude | `pane.get`, `tab.create`, `pane.send_input` | `terminal.get` for the cwd (the foreground program's, else the terminal's) and space, then window `tab.new {space, folder, argv, focus: true}` through `actions.Opener` |

The core write allowlist is `terminal.keys`, `terminal.send` and `terminal.get`. herdr-core holds keys and text until nobody has typed in that terminal for about 3 s, so writes get a 30 s timeout. A write that times out may still land once the person stops typing, and a client told it failed may press again; the long bound keeps that rare. As with the TUI, the cwd stays inside `open` and a failed `tab.new` keeps only its error code.

**insert_text has no faithful path.** `terminal.send` always presses Enter, and `terminal.keys` takes key names only: letters, digits, `/`, `'`, `space` and named keys pass, while shifted punctuation, literal spaces and non-ASCII are refused. Typing the text and submitting it would break insert_text's contract that the text waits for ENTER, so herdr-core's writer refuses it. It can come back if herdr-core gains a send that skips Enter.

### The window's socket

The control socket has no authentication, and its `do` requests reach every window command, including `terminal.kill`, `tab.close`, `app.quit`, `app.reset` and `layout.set`. `herdrapp.Window` allows only `agent.focus`, `pane.zoom` and `tab.new`, whose argv comes only from `herdr.Command.Argv` (`claude` or `claude --resume`). `herdrapp.Viewer`, which the read path uses, sends only `status`. A window that isn't open is a `herdr.Error` with code `window_closed`. The window's own error text never leaves `herdrapp`, since it may quote titles or folders: "no running terminal" becomes `not_found` (so the table reports the target gone), and anything else becomes `window_refused` in tally's words.

The window and herdr-core are reached separately, and nothing checks that the window is showing the same herdr-core session as `HC_SESSION`. A window can show another machine's spaces, so its focused space isn't a reliable test. Run tally against the session the window shows.

## doctor

With herdr-core, `tally doctor` replaces the TUI's herdr, Claude hook, sidebar sort and Agents view lines with:

- `herdr-core`: reachable, with agent and space counts. It warns when the app has no layout yet.
- `herdr window`: open, or not (informational). It promises focus, zoom and new tabs only when herdr-core is up too.
- `herdr tui`: shown only when a TUI with agents of its own is also running.

When auto lands on the TUI, doctor adds a `herdr-core` line saying why: not running, or running without a layout. A socket path that can't be worked out is a failing `herdr` line; the rest of the report still runs. doctor picks its herdr the same way the agent does, but it doesn't read the agent's own `--herdr`, so check with the same flag if you installed it with one.
