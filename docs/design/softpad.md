vibe-softpad specification
Oct 18, 2026 · @Scott J Roberts
Bottom line
vibe-softpad is an iPhone app that replaces the MacroPad when you travel. It shows agent status and drives Claude Code through the existing tally daemon, which executes every action directly against herdr's socket API. It works identically on macOS and Linux because no OS-level keystroke injection exists anywhere in the design.
Three decisions make it simple and reliable:
1. The host executes semantic actions through herdr, not keystrokes through the OS. herdr exposes pane.send_keys, pane.send_text and pane.focus on its socket. The phone sends approve_once; tally sends key 1 to that agent's pane. No Accessibility permission on macOS, no uinput on Linux, no herdr prefix chords, no keybinding config, no dependency on which window has focus.
2. Transport is TLS WebSocket over whatever IP path exists, with pinned trust from a QR pairing. Claude Code needs internet, so whenever you can vibecode, the phone and laptop share an IP path: same Wi-Fi, the phone's hotspot, or Tailscale. Bluetooth adds macOS TCC friction, flaky BlueZ behavior and a custom reconnect stack for a case (no network at all) where Claude Code doesn't run anyway.
3. One screen, no layers. A phone has room for the agent strip and the DRIVE keys at once. FLEET becomes tappable status tiles. The encoder becomes scroll buttons. TALK becomes on-device dictation through the phone's own mic.
The hardware pad keeps working unchanged. Both clients can run at once off the same state engine.
What this drops from the hardware spec: the Layout Studio, code.py generation, the keycap sheet, the herdr [keys] additions, the BG double-prefix workaround, Hold semantics and layer switching.
Architecture
The phone is a thin client. tally holds all state and all key knowledge; herdr does all terminal I/O.
The phone never touches the OS input stack on the laptop. It sends an action name and a pane id; tally validates it and calls herdr, which writes to that pane's PTY. The MacroPad path keeps its read-only herdr client and USB HID keystrokes.
Transport, discovery and security
The phone holds one TLS 1.3 WebSocket to tally on port 47824. Trust comes from a QR code scanned once, so the network path never matters for security.
Paths
At pairing, tally records every endpoint it can reach you on. On connect, the phone races all of them in parallel and keeps the first that completes TLS with the pinned certificate.
Path
When it wins
Typical latency
Bonjour on the local network (_tally._tcp)
Same Wi-Fi, or laptop on the phone's Personal Hotspot
5–20 ms
Tailscale MagicDNS name
Hotel Wi-Fi with client isolation, phone on cellular
10–150 ms (direct vs DERP relay)
Manual host:port
Anything else
varies
When every path fails, the app says which ones it tried and why.
Pairing
1. tally pair generates a self-signed ECDSA P-256 certificate (once) and a fresh 256-bit token.
2. It prints a QR code in the terminal containing {v, host_id, host_name, cert_sha256, token, endpoints[]}.
3. The phone scans it with VisionKit's DataScannerViewController and stores the record in the Keychain (kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly).
4. tally stores only sha256(token), a label and a created timestamp in ~/.config/tally/remote.json (mode 0600).
5. tally pair --list shows paired phones. tally pair --revoke <label> removes one and drops its live connection.
Authentication on every connection
• The phone pins the certificate fingerprint from the QR code and rejects anything else. No CA, no hostname check.
• The phone's first message is auth with the token. tally compares hashes in constant time.
• tally closes the socket if auth doesn't arrive within 2 s or fails. It allows 5 failures per source IP per minute.
• No other message is accepted before auth succeeds.
Why this matters
The soft pad has write access to agent panes. Text in a Claude Code prompt is one approval away from code execution. Pinned TLS plus a revocable token keeps that capability limited to your phone on any network, including hotel Wi-Fi. The listener binds to all interfaces by default; --listen restricts it (for example to the Tailscale interface only).
Action model
The phone sends action names and a target pane id. tally owns the single table that turns each action into herdr calls. The phone never encodes a key and never sends raw keys.
Targeting
• Every DRIVE action carries target: the pane id of the agent the phone shows as focused.
• tally rejects the action if that pane no longer exists or isn't an agent. It does not require the pane to be focused in herdr, so an approval always lands on the agent you saw, even if focus moved a moment earlier.
• DRIVE actions never go to a plain shell pane. /compact typed into zsh is a bug this rule prevents.
• Because actions target panes, not windows, they work while the laptop's browser or editor has focus.
DRIVE actions (to target)
Action
Button
herdr calls
interrupt
ESC
send_keys esc
cycle_mode
MODE
send_keys shift+tab
rewind
REWIND
send_keys esc, sleep 80 ms, send_keys esc
approve_once
YES
send_keys 1
approve_always
ALWAYS
send_keys 2
deny
NO
send_keys 3
autocomplete
TAB
send_keys tab
transcript
LOG
send_keys ctrl+o
compact
COMPACT
send_text "/compact", send_keys enter
submit
ENTER
send_keys enter
background
BG
send_keys ctrl+b (straight to the PTY, so no prefix collision)
scroll_up / scroll_down
▲ / ▼
send_keys up / send_keys down
insert_text
TALK
send_text <transcript> (no Enter; you press ENTER)
FLEET actions (no target, or a slot)
Action
Tile or button
Host behavior
focus_slot n
A1–A6 tile
pane.focus on agents[n-1].pane_id from the current snapshot
focus_attention
ATTN
Focus the agent blocked longest (blocked_since oldest)
next_agent / prev_agent
swipe on strip
Focus the neighbor of the focused agent in agents[], wrapping
last_pane
LAST
Focus the previous focused pane tally tracked across snapshots
zoom
ZOOM
pane.zoom on the focused pane
new_claude
NEW
tab create --cwd <focused pane cwd> --focus, then pane run <new pane> claude
resume_claude
RESUME
Same, running claude --resume
NEW and RESUME read the focused pane's cwd with pane.get inside the handler. The cwd never enters the state model, a frame, or a log line.
Write allowlist
The herdr client gains a second allowlist, active only when the remote listener runs: pane.send_keys, pane.send_text, pane.focus, pane.zoom, pane.get, tab.create, pane.run. pane.run accepts only the two fixed commands above. Every other write method stays refused.
iOS app
A SwiftUI app for iOS 26, portrait, built for a phone on a stand beside the laptop. It uses only Apple frameworks: Network, VisionKit, Speech, AVFoundation, Security and UIKit haptics. No third-party dependencies.
Screen
One screen, top to bottom:
1. Header. Link mark (live, stale, herdr offline), host name, the focused agent's slot and space label, and its state in words (A2 NEEDS APPROVAL 5m).
2. Agent strip. Six tiles in a 3×2 grid plus an ATTN tile and an overflow badge (+2). Each tile shows its slot number, state color and wait timer. Tap focuses the agent. Swipe left or right on the strip runs next_agent / prev_agent.
3. DRIVE grid. The same 3×4 keys as the hardware pad: ESC MODE REWIND / YES ALWAYS NO / TAB LOG COMPACT / ENTER TALK BG. Keys are at least 64 pt tall.
4. Utility row. NEW, RESUME, ZOOM, LAST, ▲, ▼. ▲ and ▼ auto-repeat at 10 Hz while held.
The layout is fixed in v1. A configurable layout is not worth its cost for a travel tool.
Lighting
The hardware pad's render.py rules port directly, keyed by action instead of OLED label:
Element
Lit when
Look
Slot tile N
blocked
amber, 1 Hz pulse
Slot tile G
done, unseen
green, solid
Slot tile W
working
blue, 2 s breathing
Slot tile R / U
idle / unknown
gray, dim
Focused tile
always
2 pt outline, full brightness; others at 60%
YES / ALWAYS / NO
fstate is N
amber pulse
ESC
fstate is W or N
red
BG
fstate is W
blue
ENTER
fstate is R or G
green
ATTN
any agent N, overflow included
amber pulse
Unlit keys stay fully tappable at 35% opacity. Stale or offline links dim the whole strip and show the reason in the header.
Feedback
• Press: a light impact haptic on touch-down, then a visual ack state when tally confirms (ok keeps the key white for 120 ms; error flashes it red and shows the reason in the header for 3 s).
• Cross-agent alert: replaces the double amber flash with a .warning notification haptic plus a 400 ms amber border. Same edge rule as the hardware pad: it fires on a rise in the blocked count, minus one when the focused agent itself became blocked, never on the first frame after a reconnect, and never during dictation.
TALK: on-device dictation
• Press and hold TALK to record. Transcription runs on the phone with SpeechAnalyzer and SpeechTranscriber, on-device only.
• The live transcript appears in a sheet over the DRIVE grid.
• Release sends insert_text to the focused agent. Slide off the key before release to cancel.
• Text is capped at 4,000 characters and sent without Enter. You review it in the terminal and press ENTER.
• This removes the laptop's dependency on a Right Option dictation tool, which Linux laptops typically lack.
Lifecycle
• The app runs only in the foreground. No background modes.
• While connected, it sets isIdleTimerDisabled so the screen stays on. A setting turns this off.
• On scenePhase == .active it reconnects immediately. On .background it closes the socket cleanly.
• Info.plist needs NSLocalNetworkUsageDescription, NSBonjourServices (_tally._tcp), NSCameraUsageDescription and NSSpeechRecognitionUsageDescription plus NSMicrophoneUsageDescription.
• Distribution: install from Xcode under your developer account, or TestFlight for a build that survives without a cable.
Protocol v3
JSON text messages over the WebSocket, one object per message, each with a t field. Receivers ignore unknown types and fields. Messages are capped at 16 KB. State frames stay full snapshots, never deltas.
Phone → host
{"t":"auth","v":3,"token":"<base64url>","client":"softpad/1.0"}
{"t":"action","id":417,"name":"approve_once","target":"w1:p2"}
{"t":"action","id":418,"name":"focus_slot","slot":3}
{"t":"action","id":419,"name":"insert_text","target":"w1:p2","text":"rename the handler and rerun tests"}
id is a per-connection counter. Liveness uses WebSocket ping/pong frames, not JSON.
Host → phone
{"t":"hello","v":3,"host":"mbp","host_id":"7f3c…","tally":"0.3.0"}
{"t":"state","v":3,"seq":1042,
 "slots":[{"s":"W","pane":"w1:p1"},{"s":"N","pane":"w1:p2","since":312},null,
          {"s":"G","pane":"w2:p1"},{"s":"R","pane":"w2:p3"},null],
 "focus":2,"fpane":"w1:p2","fstate":"N","overflow":{"N":1,"blocked_since":[45]},
 "herdr":true,"space":"vibe-macro"}
{"t":"ack","id":417,"ok":true}
{"t":"ack","id":418,"ok":false,"err":"target_gone"}
Changes from v2:
• Each slot carries its herdr pane id, so actions can target it.
• since is seconds a blocked agent has waited, measured by tally from snapshot transitions. Timers survive phone reconnects and app restarts.
• fpane is the focused agent's pane id, the default DRIVE target.
• The 512-byte cap and the ping/hello discovery handshake go away. TLS and WebSocket framing replace them.
Slot codes and colors keep the v2 meanings: N blocked, G done, W working, R idle, U unknown, null empty.
Ack errors
err
Meaning
target_gone
The pane closed or stopped being an agent
herdr_offline
tally can't reach herdr
bad_action
Unknown action name or missing field
expired
Reserved. In v1 the phone enforces freshness: it drops any action it can't write within 500 ms of the tap and never retries one
herdr_error
herdr rejected the call; the message carries herdr's text
Cadence
• tally sends state on change (50 ms debounce, as today), on auth success, and every 5 s.
• The phone pings every 2 s. It marks the link stale after 5 s without a pong or frame and starts reconnecting.
• Reconnect backoff: 0, 250 ms, 500 ms, 1 s, then 2 s steady.
Host daemon changes (Go)
The soft pad extends tally rather than adding a second daemon. The actor, snapshot logic, slot mapping and herdr client stay as built. New code lives in three packages and one CLI command.
New packages
Package
Role
internal/actions
The action table: name → herdr calls. Validates target, enforces agent-only DRIVE targets, runs the REWIND gap, reads cwd for NEW/RESUME. Pure apart from a herdr.Writer interface, so tests use a fake.
internal/remote
TLS listener, WebSocket sessions, auth, per-IP rate limit, v3 frame encoding, Bonjour advertisement. Subscribes to the actor's frames exactly like device.Link.
internal/pairing
Certificate generation, token issue and revoke, remote.json, terminal QR rendering.
internal/herdr gains a Writer type built only by tally serve --remote. It wraps the write allowlist. The read-only Client and its allowlist stay untouched, so the serial path keeps its guarantee.
internal/slots adds pane per slot, fpane, and blocked_since, tracked per pane id in the actor across snapshots.
CLI
tally serve [--remote] [--listen ADDR] [--no-device] [--port PATH] [--slots N] [--verbose]
tally pair [--label NAME] | --list | --revoke NAME
tally act <action> [--target PANE] [--slot N] [--text STR]
tally install-agent [--remote] [...]
tally doctor
• --remote turns on the listener on :47824. --no-device skips pad discovery on the road.
• tally act runs any action from the terminal through the same table. It is the test harness for M1 and a debugging tool afterward.
Dependencies
• github.com/coder/websocket for WebSocket.
• github.com/grandcat/zeroconf for Bonjour advertisement on both OSes.
• github.com/mdp/qrterminal/v3 for the pairing QR code.
• Standard library crypto/tls, crypto/ecdsa, crypto/subtle for everything else.
macOS
• LaunchAgent as today (local.tally), with --remote in ProgramArguments.
• No TCC permissions needed. Network listening triggers the application firewall prompt once if the firewall is on; doctor checks for an allow rule.
Linux
• install-agent writes a systemd user unit at ~/.config/systemd/user/tally.service (Restart=always, RestartSec=2) and runs systemctl --user enable --now tally. Logs go to the journal.
• herdr's socket path resolution is identical, under ~/.config/herdr/.
• If avahi-daemon holds port 5353, zeroconf still advertises through SO_REUSEPORT. Doctor verifies the advertisement resolves with avahi-browse when it is installed.
• Firewalls: doctor detects active ufw or firewalld and prints the exact rule to open 47824/tcp.
• No root, no udev rules, no group membership.
Doctor additions
1. remote listener: bound, which addresses, TLS cert present and readable.
2. paired phones: count and labels; WARN at zero.
3. bonjour: advertisement visible on the local network.
4. tailscale: tailscale status --json reachable, MagicDNS name present (informational).
5. firewall: port reachable from the LAN interface.
6. write path: tally act dry run against a scratch pane succeeds.
Failure modes
Failure
Behavior
Phone loses the link
Stale after 5 s: strip dims, header reads TALLY OFFLINE, keys disable. Reconnect races all endpoints; the first frame restores everything.
Action in flight when the link drops
Discarded. Never retried, because approve_once sent twice can approve a second prompt.
Target pane closed
target_gone ack, red flash, header names the reason.
Focus changed just before a tap
The action still lands on the pane the phone showed, because it carries target.
herdr stopped
herdr:false frames, HERDR OFFLINE, actions return herdr_offline.
Hotel Wi-Fi isolates clients
The LAN race fails fast; Tailscale wins.
No Tailscale and isolated Wi-Fi
Put the laptop on the phone's hotspot; Bonjour finds it.
Phone screen locks
Socket closes; unlocking reconnects in under 1 s on a LAN path.
Token leaked
tally pair --revoke drops the session and invalidates the token. Re-pair with a new QR.
Pad and phone both connected
Both receive the same frames. The pad still types through HID; the phone acts through herdr.
Invariants
• The host owns every key encoding. The phone sends action names, never keys.
• DRIVE actions carry an explicit agent pane target and never reach a plain shell.
• Actions are never retried. A dropped action stays dropped.
• Every frame comes from one snapshot. Events only invalidate, as today.
• Slot n = agents[n-1], never re-sorted.
• No titles, cwds or terminal output leave the host. Frames carry ids, state codes, timers and the focused space label only.
• The serial path stays read-only. Only --remote constructs the herdr Writer.
• Nothing is accepted before auth.
Testing
• Go: action table against a fake herdr.Writer (every action, agent-only targeting, target_gone, REWIND gap, NEW/RESUME cwd handling without leakage); pairing round trip; auth (bad token, late auth, rate limit, revoke drops a live session); v3 frame encoding; Bonjour advertisement in-process; blocked_since across snapshots.
• Swift: lighting rules and the alert edge rule as pure functions with XCTest, ported case for case from test_render.py; endpoint race with fake connections; pinning rejects a wrong fingerprint; never-retry behavior.
• End to end: tally serve --remote against a live herdr session with two Claude Code agents, driven from the phone on (a) the same Wi-Fi, (b) the phone's hotspot, (c) Tailscale over cellular. Run on one Mac and one Linux laptop.
Milestones
Each milestone lands as its own PR off main.
1. M0: verify herdr input (gate). On macOS and Linux, confirm pane.send_keys produces the right behavior in Claude Code for esc, esc esc, shift+tab, ctrl+o, ctrl+b, 1/2/3 at an approval prompt, and arrows. Confirm the exact socket method names for focus, zoom, tab creation and run. If any key fails through the PTY, record the fallback encoding in the action table before M1.
2. M1: action table and tally act. All actions runnable from the terminal. Write allowlist in place.
3. M2: remote listener and pairing. TLS, auth, v3 frames, Bonjour, tally pair. Tested with a Go test client.
4. M3: iOS app core. Pairing scan, endpoint race, state rendering, DRIVE and FLEET actions with acks.
5. M4: dictation, haptics, alert.
6. M5: Linux service and doctor. systemd unit, firewall and Bonjour checks, end-to-end runs on both OSes.
Not in v1
• A configurable layout or layout editor.
• Showing pane output on the phone. The laptop screen sits next to it, and keeping output on the host preserves the privacy invariant.
• Approving from a slot tile without focusing it. The protocol already supports it through target; the UI waits until v1 proves out.
• Apple Watch and iPad layouts.
• Bluetooth transport.