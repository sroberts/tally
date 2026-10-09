# vibe-macropad

A keypad for driving [Claude Code](https://claude.com/claude-code) from an Adafruit MacroPad RP2040, with status lights that show what each agent is doing.

<img src="docs/macropad.jpg" alt="The MacroPad on the DRIVE layer: ESC, MODE, REWIND, YES, ALWAYS, NO, COMPACT, LOG, TAB, ENTER, TALK and BG keys, lit, with the layer's labels on the OLED" width="420">

The pad is a plain USB keyboard with two layers; press the encoder to switch between them.

- **DRIVE** sends keystrokes straight to Claude Code: ESC, MODE, REWIND, YES, ALWAYS, NO, TAB, LOG, COMPACT, ENTER, TALK (holds Right Option for voice) and BG.
- **FLEET** drives the herdr terminal multiplexer through its prefix key: A1–A6 jump to agents in herdr's sidebar order, plus ATTN, NEW, RESUME, ZOOM, LAST and ENTER. The encoder turns through agents on this layer and scrolls on DRIVE.

Each keycap carries both labels, DRIVE on top and FLEET below; `macropad-keycap-sheet.pdf` has the printable inserts.

## Tally

Tally lights the FLEET keys with each herdr agent's state: amber blinking when an agent needs you, green when it finished and you haven't looked, blue breathing while it works, dim white when idle. A Go CLI reads herdr's socket (read-only) and sends frames to the pad over a second USB serial port; the pad only renders them. The design is in [`Tally MacroPad Status Lights for Claude Code.md`](<Tally MacroPad Status Lights for Claude Code.md>).

```bash
go build -o ~/.local/bin/tally ./cmd/tally
tally serve                          # keep the pad's lights and OLED in step with herdr
tally status                         # herdr's agents in FLEET key order
```

### Reading the OLED

Once Tally is running, the screen shows context instead of key names (the keycaps have those):

```
DRIVE A1 vibe-macro *
A1 WORKING
A5 WAITING 3m  DONE 4
1W 2W 3R 4R 5N 6G  +1
```

1. **Layer, focused agent, its space name, link.** `A1` is the agent you're looking at (`+` when it's beyond the six keys). The last character is the link: `*` live, `x` Tally has stopped sending (`TALLY OFFLINE`), `h` herdr isn't running (`HERDR OFFLINE`).
2. **What the focused agent is doing**, in words. `NEEDS APPROVAL 5m` says how long it has waited.
3. **Who else needs you.** Agents waiting on you, longest first, with how long (`A5 WAITING 3m`, or `WAIT A3 4m A1 1m` for several), `+1 WAIT` for waiting agents beyond the six keys, and `DONE 4` for agents that finished while you were elsewhere. Wait times count from when the pad first saw the agent waiting.
4. **Every slot at a glance.** One entry per FLEET key: the slot number (A1–A6) followed by a state code, then `+N` for agents beyond the six keys.

| Code | Means | Key light |
| --- | --- | --- |
| `N` | **Needs you**: a permission prompt or a question is waiting | amber, blinking |
| `G` | **Done** ("green"): finished a turn you haven't looked at yet | green |
| `W` | **Working** on a turn | blue, breathing |
| `R` | **Ready**: idle, and you've seen its result | dim white |
| `U` | **Unknown**: herdr can't tell yet (agent still starting) | faint white |
| `-` | No agent in that slot | off |

The same states drive the FLEET key colors, so `5N` on the screen means the A5 key is blinking amber.

## Layout

| Path | What it is |
| --- | --- |
| `MacroPad Layout Studio.html` | Browser app and source of truth for the keymap; it generates `code.py` and the matching herdr config. |
| `firmware/` | CircuitPython: `code.py` (generated), `boot.py`, `tally_link.py`, `render.py`. Copy all of them to the `CIRCUITPY` drive. |
| `cmd/tally`, `internal/` | The Tally CLI. |
| `keycap_sheet.py` | Renders the keycap PDF. |

## Development

```bash
go test ./...   # Go tests
uv run pytest   # firmware tests under CPython
```

Work follows GitHub Flow: branch, open a PR, merge with a merge commit.
