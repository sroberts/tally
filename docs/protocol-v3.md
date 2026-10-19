# Remote protocol v3, as built

The phone holds one TLS 1.3 WebSocket to tally, `wss://<endpoint>:47824/` (any path). JSON text
messages, one object each, every one with a `t` field. Messages are capped at 16 KB; a bigger one
closes the session (status 1009). Receivers ignore unknown types and fields. Liveness is WebSocket
ping/pong; tally also pings every 5 s and drops a phone that doesn't answer within 5 s.

## Pairing QR payload

`tally pair` prints a QR code of this JSON (`tally pair --json` also prints it as text):

```json
{"v":3,"host_id":"7f3c…","host_name":"mbp","cert_sha256":"<64 lowercase hex>","token":"<43 chars>",
 "endpoints":[{"kind":"bonjour","name":"tally-7f3c0011"},
              {"kind":"tailscale","host":"mbp.tail1234.ts.net","port":47824},
              {"kind":"host","host":"192.168.1.20","port":47824}]}
```

- `cert_sha256`: SHA-256 of the DER leaf certificate. Pin it; there is no CA and no hostname check.
- `token`: 32 random bytes, base64url without padding. tally stores only its SHA-256.
- `bonjour.name`: the `_tally._tcp` instance in `local.`, always `tally-` plus the first 8 characters of
  `host_id`. The TXT record is `v=3`, `id=<host_id>`. tally advertises only when it listens on every
  interface.
- `tailscale` appears when `tailscale status --json` reports a MagicDNS name; `host` entries are the
  LAN IPv4 addresses at pairing time.

## Phone → host

```json
{"t":"auth","v":3,"token":"<base64url>","client":"pad/1.0"}
{"t":"action","id":417,"name":"approve_once","target":"w1:p2"}
{"t":"action","id":418,"name":"focus_slot","slot":3}
{"t":"action","id":419,"name":"insert_text","target":"w1:p2","text":"rename the handler"}
```

`auth` must be the first message and arrive within 2 s; anything else closes the socket with status
1008. Five failed auths from one source IP within a minute make tally refuse that IP's connections
with HTTP 429 until the minute passes. `id` is the phone's per-connection counter.

Actions:

| kind | names | needs |
|---|---|---|
| DRIVE | `interrupt` `cycle_mode` `rewind` `approve_once` `approve_always` `deny` `autocomplete` `transcript` `compact` `submit` `background` `scroll_up` `scroll_down` | `target` |
| DRIVE | `insert_text` | `target`, `text` (≤ 4,000 characters; control characters become spaces) |
| FLEET | `focus_slot` | `slot` (from 1) |
| FLEET | `focus_attention` `next_agent` `prev_agent` `last_pane` `zoom` `new_claude` `resume_claude` | — |

A session runs its actions one at a time, in order.

## Host → phone

```json
{"t":"hello","v":3,"host":"mbp","host_id":"7f3c…","tally":"0.3.0"}
{"t":"state","v":3,"seq":1042,
 "slots":[{"s":"W","pane":"w1:p1"},{"s":"N","pane":"w1:p2","since":312},null,
          {"s":"G","pane":"w2:p1"},{"s":"R","pane":"w2:p3"},null],
 "focus":2,"fpane":"w1:p2","fstate":"N","overflow":{"N":1,"blocked_since":[45]},
 "herdr":true,"space":"vibe-macro"}
{"t":"ack","id":417,"ok":true}
{"t":"ack","id":418,"ok":false,"err":"target_gone"}
{"t":"ack","id":420,"ok":false,"err":"herdr_error","msg":"<herdr's text>"}
```

- `hello` comes once, right after auth, then a `state`.
- `state` is always a full snapshot. `slots` has one entry per slot (`null` when empty). `since` is
  present only on `N` slots: whole seconds since tally first saw that agent blocked. It is computed
  when the frame is sent, so the phone counts up locally between frames.
- `focus` is the 1-based slot of the focused agent, `0` when it is beyond the slots, `null` when the
  focus isn't an agent. `fpane` and `fstate` are that agent's pane and code (also beyond the slots),
  `null` otherwise.
- `overflow` counts agents beyond the slots by code; `blocked_since` lists the wait of each blocked
  one, in sidebar order, and is omitted when empty.
- `space` is the focused agent's workspace label, at most 32 characters; omitted when empty.
- `herdr:false` frames have every slot `null` and no focus.
- `seq` rises with every change. tally sends `state` on change (50 ms debounce), after auth, and
  every 5 s; a resend of an unchanged state keeps its `seq`.

Codes: `N` blocked, `G` done and unseen, `W` working, `R` idle, `U` unknown.

## Ack errors

| err | meaning |
|---|---|
| `target_gone` | the pane closed or isn't an agent; or a FLEET action had nothing to act on (empty slot, no agent waiting, no previous pane) |
| `herdr_offline` | tally can't reach herdr |
| `bad_action` | unknown action name or missing or invalid field |
| `herdr_error` | herdr rejected the call; `msg` carries herdr's text (`busy` when 16 actions are already queued) |
| `expired` | reserved; the phone drops actions it can't write within 500 ms and never retries |

Close statuses: 1008 auth failed or phone revoked, 1009 message too big, 1001 tally stopping or no
pong.

## The browser pad

`tally serve --web` carries the same protocol to a browser at `wss://<host>:47825/ws`, with one difference: there is no `auth` message. The browser signs in with a password first, and the `__Host-tally` cookie it gets is checked before the upgrade; tally then sends `hello` and `state` and takes actions exactly as above. A browser that is signed out or revoked is closed with status 1008. See `docs/design/browser-pad.md`.
