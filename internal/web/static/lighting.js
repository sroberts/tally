// Lighting and readout for the browser pad: a port of the hardware pad's
// render.py, keyed by action instead of OLED label. Pure functions of the
// frame and the link; lighting_test.mjs holds the same cases as
// vibe-macropad's tests/test_render.py.

export const unfocused = 0.6;
// Opacity of an unlit key's plate. Unlit keys stay tappable.
export const unlit = 0.35;
// The cross-agent alert's amber border, in ms.
export const alertMS = 400;

// Link: "none" (no frame yet), "live" or "stale".
export function isLive(frame, link) {
  return link === "live" && frame != null && frame.herdr !== false;
}

export function stripDimmed(frame, link) {
  return !isLive(frame, link);
}

// A glow: {hue, pattern, level}. Hues: amber green blue red gray.
// Patterns: solid, pulse (1 Hz on/off), breathe (2 s cycle).
const glow = (hue, pattern = "solid", level = 1) => ({ hue, pattern, level });

export function slotGlow(code) {
  switch (code) {
    case "N": return glow("amber", "pulse");
    case "G": return glow("green");
    case "W": return glow("blue", "breathe");
    case "R": return glow("gray", "solid", 0.6);
    case "U": return glow("gray", "solid", 0.3);
    default: return null;
  }
}

// Slot tile n (0-based): {glow, focused, emphasis}.
export function tile(n, frame, link) {
  if (!isLive(frame, link)) return { glow: null, focused: false, emphasis: unfocused };
  const slot = n < frame.slots.length ? frame.slots[n] : null;
  const focused = frame.focus === n + 1;
  return { glow: slotGlow(slot?.s), focused, emphasis: focused ? 1 : unfocused };
}

// Blocked agents, overflow included.
export function waiting(frame) {
  if (!frame) return 0;
  return frame.slots.filter((s) => s?.s === "N").length + (frame.overflow.N ?? 0);
}

export function overflowTotal(frame) {
  if (!frame) return 0;
  let n = 0;
  for (const [k, v] of Object.entries(frame.overflow)) if (k !== "blocked_since" && typeof v === "number") n += v;
  return n;
}

// ATTN pulses while any agent waits, overflow included.
export function attn(frame, link) {
  if (!isLive(frame, link) || waiting(frame) === 0) return null;
  return glow("amber", "pulse");
}

// DRIVE keys light when they matter for the focused agent.
export function drive(action, frame, link) {
  if (!isLive(frame, link)) return null;
  const f = frame.fstate;
  switch (action) {
    case "interrupt": return f === "W" || f === "N" ? glow("red") : null;
    case "approve_once": case "approve_always": case "deny":
      return f === "N" ? glow("amber", "pulse") : null;
    case "submit": return f === "R" || f === "G" ? glow("green") : null;
    case "background": return f === "W" ? glow("blue") : null;
    default: return null;
  }
}

// 1 Hz on/off, on for the first half of each second.
export const blink = (ms) => ms % 1000 < 500;

// 2 s cycle between 15% and 35%, as the pad breathes.
export const breathe = (ms) => 0.25 - 0.10 * Math.cos((2 * Math.PI * (ms % 2000)) / 2000);

// How much of a glow shows at time ms, 0...1. Under reduced motion
// patterns hold steady.
export function intensity(g, ms, reduceMotion = false) {
  let p;
  switch (g.pattern) {
    case "pulse": p = reduceMotion || blink(ms) ? 1 : 0; break;
    case "breathe": p = reduceMotion ? 1 : 0.55 + (0.45 * (breathe(ms) - 0.15)) / 0.2; break;
    default: p = 1;
  }
  return p * g.level;
}

// The cross-agent alert: fires when another agent starts waiting on you —
// a rise in the blocked count, minus one when the focused agent itself
// became blocked; never on the first frame after a (re)connect and never
// while talk is open.
export class Alert {
  constructor() {
    this.waiting = null; // blocked agents in the last live frame; null: no baseline
    this.focused = false;
    this.start = null;
  }

  // Feed every frame and link change; returns true when a new alert starts.
  update(frame, link, talk, now) {
    if (talk) this.start = null;
    if (!frame || link !== "live" || frame.herdr === false) {
      this.waiting = null;
      return false;
    }
    const w = waiting(frame), f = frame.fstate === "N";
    let fired = false;
    if (this.waiting !== null && !talk) {
      let added = w - this.waiting;
      if (f && !this.focused) added -= 1; // the agent in front of you: its keys already pulse
      if (added > 0) { this.start = now; fired = true; }
    }
    this.waiting = w;
    this.focused = f;
    return fired;
  }

  cancel() { this.start = null; }

  showing(now) {
    if (this.start === null) return false;
    const t = now - this.start;
    return t >= 0 && t < alertMS;
  }
}

// --- Readout ---------------------------------------------------------------

const words = { N: "needs approval", G: "done", W: "working", R: "ready", U: "unknown" };
export const shortWords = { N: "needs you", G: "done", W: "working", R: "ready", U: "unknown" };

// "42s", "5m", "1h05".
export function wait(seconds) {
  const s = Math.max(0, Math.floor(seconds));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}`;
}

// "a2", "a+" beyond the slots, null when the focus isn't an agent.
export function slotName(frame) {
  if (frame.focus == null) return null;
  return frame.focus >= 1 ? `a${frame.focus}` : "a+";
}

function focusedSlot(frame) {
  const f = frame.focus;
  if (f == null || f < 1 || f > frame.slots.length) return null;
  return frame.slots[f - 1];
}

// The focused agent's wait, plus the time since the frame arrived.
export function focusedWait(frame, elapsed) {
  const s = focusedSlot(frame);
  if (frame.fstate !== "N" || s?.since == null) return null;
  return s.since + Math.floor(Math.max(0, elapsed));
}

export function slotWait(slot, elapsed) {
  if (slot?.s !== "N" || slot.since == null) return null;
  return slot.since + Math.floor(Math.max(0, elapsed));
}

export function overflowWait(frame, elapsed) {
  const b = frame.overflow.blocked_since;
  if (!Array.isArray(b) || b.length === 0) return null;
  return Math.max(...b) + Math.floor(Math.max(0, elapsed));
}

// Status line: "a2 needs approval 5m", or the reason nothing is live.
export function status(frame, link, elapsed) {
  if (link === "none") return "connecting";
  if (link === "stale") return "tally offline";
  if (!frame) return "waiting for state";
  if (frame.herdr === false) return "herdr offline";
  const slot = slotName(frame);
  if (slot === null || frame.fstate == null) {
    return frame.slots.some((s) => s) || overflowTotal(frame) > 0 ? "no agent focused" : "no agents";
  }
  let out = `${slot} ${words[frame.fstate] ?? "unknown"}`;
  const w = focusedWait(frame, elapsed);
  if (w !== null) out += ` ${wait(w)}`;
  return out;
}

// --- Frames ----------------------------------------------------------------

const codes = new Set(["N", "G", "W", "R", "U"]);

// Normalizes a state message leniently: a field with the wrong type falls
// back to its default rather than failing the frame, as the pad does.
export function parseFrame(m) {
  const int = (v) => (Number.isInteger(v) ? v : null);
  const slots = Array.isArray(m.slots)
    ? m.slots.map((s) => (s && typeof s === "object" && typeof s.s === "string"
      ? { s: codes.has(s.s) ? s.s : "U", pane: typeof s.pane === "string" ? s.pane : "", since: int(s.since) }
      : null))
    : [null, null, null, null, null, null];
  const overflow = {};
  if (m.overflow && typeof m.overflow === "object" && !Array.isArray(m.overflow)) {
    for (const [k, v] of Object.entries(m.overflow)) {
      if (k === "blocked_since") {
        if (Array.isArray(v)) overflow.blocked_since = v.filter(Number.isInteger);
      } else if (codes.has(k) && Number.isInteger(v)) {
        overflow[k] = v;
      }
    }
  }
  return {
    seq: int(m.seq) ?? 0,
    slots,
    focus: int(m.focus),
    fpane: typeof m.fpane === "string" ? m.fpane : null,
    fstate: typeof m.fstate === "string" ? (codes.has(m.fstate) ? m.fstate : "U") : null,
    overflow,
    herdr: typeof m.herdr === "boolean" ? m.herdr : true,
    space: typeof m.space === "string" ? m.space : null,
  };
}

// --- Actions ---------------------------------------------------------------

const driveActions = new Set(["interrupt", "cycle_mode", "rewind", "approve_once", "approve_always", "deny",
  "autocomplete", "transcript", "compact", "submit", "insert_text", "background", "scroll_up", "scroll_down"]);

export const insertTextLimit = 4000;

// The wire message, or null when a DRIVE action has no agent to target.
export function actionMessage(id, action, target, { slot, text } = {}) {
  if (driveActions.has(action)) {
    if (!target) return null;
    const m = { t: "action", id, name: action, target };
    if (text != null) m.text = [...text].slice(0, insertTextLimit).join("");
    return m;
  }
  const m = { t: "action", id, name: action };
  if (slot != null) m.slot = slot;
  return m;
}

const ackReasons = { target_gone: "target gone", herdr_offline: "herdr offline", bad_action: "bad action",
  expired: "expired", herdr_error: "herdr error" };

// Lowercase header text for a failed ack.
export function ackReason(a) {
  if (a.err === "herdr_error") return a.msg ? `herdr: ${a.msg}` : "herdr error";
  return ackReasons[a.err] ?? "failed";
}
