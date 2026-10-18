# M0: herdr input check

herdr 0.9.3, protocol 22, macOS. Run 2026-10-18 in a scratch tab with `/bin/cat -v` in the pane.

## Bytes reaching the PTY through `pane.send_keys`

| key | bytes | Claude Code meaning |
|---|---|---|
| `esc` | `^[` | interrupt; twice = rewind |
| `shift+tab` | `^[[Z` | cycle mode |
| `ctrl+o` | `^O` | transcript (a cooked-mode tty eats `^O` as VDISCARD; Claude Code reads raw, so it arrives) |
| `ctrl+b` | `^B` | background, straight to the PTY, no herdr prefix involved |
| `tab` | `^I` | autocomplete |
| `up` / `down` | `^[[A` / `^[[B` | scroll |
| `1` `2` `3` | `1` `2` `3` | approval choices |

All encodings are the expected ones, so the action table needs no fallbacks.
Not yet checked: the same keys against a live Claude Code approval prompt, and Linux.

## Socket methods

| spec name | herdr method | params |
|---|---|---|
| send_keys | `pane.send_keys` | `{pane_id, keys: [..]}` |
| send_text | `pane.send_text` | `{pane_id, text}` |
| focus | `pane.focus` | `{pane_id}` |
| zoom | `pane.zoom` | `{pane_id, mode: "toggle"}` |
| get | `pane.get` | `{pane_id}` → `{pane: {cwd, ...}}` |
| tab create | `tab.create` | `{cwd, focus, workspace_id}` → `{tab, root_pane}` |
| pane run | **no socket method**: the CLI's `pane run` is `pane.send_input` | `{pane_id, text, keys: ["enter"]}` |

So the write allowlist holds `pane.send_input` in place of `pane.run`, and the action table only ever
passes it the two fixed commands `claude` and `claude --resume`.
