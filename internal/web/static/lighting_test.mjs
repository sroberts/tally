// The browser pad's lighting, case for case with the hardware pad's
// tests/test_render.py. Run: node --test internal/web/static/lighting_test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import * as L from "./lighting.js";

const blinkOn = 100; // ms into a second: blink is on
const blinkOff = 600; // ms into a second: blink is off
const G = (hue, pattern = "solid", level = 1) => ({ hue, pattern, level });
const amberPulse = G("amber", "pulse");

function frame({ slots = [], focus = null, fstate = null, overflow = {}, herdr = true } = {}) {
  const s = slots.map((c, i) => (c ? { s: c, pane: `w1:p${i + 1}`, since: null } : null));
  while (s.length < 6) s.push(null);
  return { seq: 0, slots: s, focus, fpane: focus != null ? `w1:p${focus}` : null, fstate, overflow, herdr, space: null };
}

const allActions = ["interrupt", "cycle_mode", "rewind", "approve_once", "approve_always", "deny", "autocomplete",
  "transcript", "compact", "submit", "insert_text", "background", "scroll_up", "scroll_down", "focus_slot",
  "focus_attention", "next_agent", "prev_agent", "last_pane", "zoom", "new_claude", "resume_claude"];

test("no frame lights nothing", () => {
  for (const a of allActions) assert.equal(L.drive(a, null, "none"), null, a);
  for (let n = 0; n < 6; n++) assert.equal(L.tile(n, null, "none").glow, null);
  assert.equal(L.attn(null, "none"), null);
});

test("drive follows the focused agent", () => {
  const esc = G("red"), enter = G("green"), bg = G("blue");
  const rows = [
    //      ESC  YES/ALWAYS/NO  ENTER  BG
    ["N", esc, amberPulse, null, null],
    ["W", esc, null, null, bg],
    ["G", null, null, enter, null],
    ["R", null, null, enter, null],
    ["U", null, null, null, null],
    [null, null, null, null, null],
  ];
  for (const [fstate, e, approve, ent, b] of rows) {
    const f = frame({ fstate });
    assert.deepEqual(L.drive("interrupt", f, "live"), e, String(fstate));
    for (const a of ["approve_once", "approve_always", "deny"]) assert.deepEqual(L.drive(a, f, "live"), approve, String(fstate));
    assert.deepEqual(L.drive("submit", f, "live"), ent, String(fstate));
    assert.deepEqual(L.drive("background", f, "live"), b, String(fstate));
    for (const a of ["cycle_mode", "rewind", "autocomplete", "transcript", "compact", "insert_text"]) {
      assert.equal(L.drive(a, f, "live"), null, `${fstate} ${a}`);
    }
  }
});

test("slot states on the focused tile", () => {
  for (const [code, want] of [["G", G("green")], ["R", G("gray", "solid", 0.6)], ["U", G("gray", "solid", 0.3)], [null, null]]) {
    const t = L.tile(0, frame({ slots: [code], focus: 1 }), "live");
    assert.deepEqual(t.glow, want, String(code));
    assert.equal(t.focused, true);
    assert.equal(t.emphasis, 1);
  }
});

test("blocked pulses at 1 Hz", () => {
  const g = L.tile(0, frame({ slots: ["N"], focus: 1 }), "live").glow;
  assert.deepEqual(g, amberPulse);
  assert.equal(L.intensity(g, blinkOn), 1);
  assert.equal(L.intensity(g, blinkOff), 0);
  assert.equal(L.intensity(g, 1000 + blinkOn), 1);
});

test("working breathes over two seconds", () => {
  const g = L.tile(0, frame({ slots: ["W"], focus: 1 }), "live").glow;
  assert.deepEqual(g, G("blue", "breathe"));
  const ms = Array.from({ length: 40 }, (_, i) => i * 50);
  const raw = ms.map(L.breathe);
  assert.ok(Math.abs(Math.min(...raw) - 0.15) < 0.001);
  assert.ok(Math.abs(Math.max(...raw) - 0.35) < 0.001);
  const shown = ms.map((m) => L.intensity(g, m));
  assert.ok(Math.abs(Math.min(...shown) - 0.55) < 0.001);
  assert.ok(Math.abs(Math.max(...shown) - 1) < 0.001);
  assert.ok(Math.abs(L.intensity(g, 0) - L.intensity(g, 2000)) < 0.0001);
});

test("unfocused tiles at 60 percent", () => {
  const f = frame({ slots: ["G", "G"], focus: 2 });
  const a1 = L.tile(0, f, "live"), a2 = L.tile(1, f, "live");
  assert.equal(a1.emphasis, 0.6);
  assert.equal(a1.focused, false);
  assert.equal(a2.emphasis, 1);
  assert.equal(a2.focused, true);
});

test("tiles follow slot index, not position", () => {
  const f = frame({ slots: [null, null, null, null, null, "G"], focus: 6, fstate: "G" });
  assert.deepEqual(L.tile(5, f, "live").glow, G("green"));
  assert.equal(L.tile(0, f, "live").glow, null);
  assert.deepEqual(L.drive("submit", f, "live"), G("green"));
});

test("attn pulses when any agent waits", () => {
  assert.equal(L.attn(frame(), "live"), null);
  const g = L.attn(frame({ slots: [null, null, "N"] }), "live");
  assert.deepEqual(g, amberPulse);
  assert.equal(L.intensity(g, blinkOn), 1);
  assert.equal(L.intensity(g, blinkOff), 0);
});

test("attn counts overflow", () => {
  assert.deepEqual(L.attn(frame({ overflow: { N: 1 } }), "live"), amberPulse);
});

test("enter lights when ready or done", () => {
  for (const [fstate, want] of [["R", G("green")], ["G", G("green")], ["W", null], ["N", null], [null, null]]) {
    assert.deepEqual(L.drive("submit", frame({ fstate }), "live"), want, String(fstate));
  }
});

test("other fleet keys are quiet", () => {
  for (const a of ["new_claude", "resume_claude", "zoom", "last_pane", "scroll_up", "scroll_down"]) {
    assert.equal(L.drive(a, frame({ fstate: "N" }), "live"), null, a);
  }
});

test("stale link dims the strip", () => {
  const f = frame({ slots: Array(6).fill("N"), focus: 1 });
  assert.equal(L.stripDimmed(f, "stale"), true);
  assert.equal(L.attn(f, "stale"), null);
  for (let n = 0; n < 6; n++) assert.equal(L.tile(n, f, "stale").glow, null);
  assert.equal(L.stripDimmed(f, "live"), false);
});

test("herdr offline dims the strip", () => {
  const f = frame({ slots: Array(6).fill("N"), herdr: false });
  assert.equal(L.stripDimmed(f, "live"), true);
  assert.equal(L.attn(f, "live"), null);
  assert.equal(L.tile(0, f, "live").glow, null);
});

test("bad frame fields do not crash", () => {
  const f = L.parseFrame({ t: "state", v: 3, slots: "junk", overflow: [1], focus: "x" });
  for (let n = 0; n < 6; n++) assert.equal(L.tile(n, f, "live").glow, null);
  assert.equal(L.attn(f, "live"), null);
  const short = L.parseFrame({ slots: [{ s: "N", pane: "w1:p1" }], focus: 1 });
  assert.equal(L.tile(5, short, "live").glow, null);
  assert.equal(L.tile(5, short, "live").emphasis, 0.6);
});

test("approval keys pulse when the focused agent waits", () => {
  const f = frame({ fstate: "N" });
  for (const a of ["approve_once", "approve_always", "deny"]) {
    const g = L.drive(a, f, "live");
    assert.equal(L.intensity(g, blinkOn), 1);
    assert.equal(L.intensity(g, blinkOff), 0);
  }
});

test("drive is unlit unless live", () => {
  assert.equal(L.drive("approve_once", frame({ fstate: "N" }), "stale"), null);
  assert.equal(L.drive("approve_once", frame({ fstate: "N", herdr: false }), "live"), null);
});

test("reduced motion holds patterns steady", () => {
  assert.equal(L.intensity(amberPulse, blinkOff, true), 1);
  assert.equal(L.intensity(G("blue", "breathe"), 0, true), 1);
  assert.equal(L.intensity(G("gray", "solid", 0.3), 0, true), 0.3);
});

// --- Alert ---------------------------------------------------------------------

function after(frames, talk = false) {
  const a = new L.Alert();
  let now = 0, fired = false;
  for (const f of frames) {
    now += 1000;
    fired = a.update(f, "live", talk, now);
  }
  return [a, now, fired];
}

test("alert shows for 400 ms when another agent starts waiting", () => {
  const [a, t, fired] = after([frame({ slots: ["W", "R"], focus: 1, fstate: "W" }), frame({ slots: ["W", "N"], focus: 1, fstate: "W" })]);
  assert.equal(fired, true);
  assert.deepEqual([0, 99, 200, 399, 400, 1000].map((d) => a.showing(t + d)), [true, true, true, true, false, false]);
});

test("alert counts overflow", () => {
  const [a, t] = after([frame({ overflow: { R: 1 } }), frame({ overflow: { N: 1 } })]);
  assert.equal(a.showing(t), true);
});

test("alert is edge triggered", () => {
  const waitingF = frame({ slots: ["N", "R"], focus: 2, fstate: "R" });
  const [a, t] = after([frame({ slots: ["R", "R"] }), waitingF]);
  assert.equal(a.showing(t), true);
  assert.equal(a.update(waitingF, "live", false, t + 5000), false);
  assert.equal(a.showing(t + 5000), false);
});

test("alert ignores the focused agent", () => {
  const [a, t, fired] = after([frame({ slots: ["W"], focus: 1, fstate: "W" }), frame({ slots: ["N"], focus: 1, fstate: "N" })]);
  assert.equal(fired, false);
  assert.equal(a.showing(t), false);
});

test("alert ignores focus moving off a waiting agent", () => {
  const [a, t] = after([frame({ slots: ["N", "R"], focus: 1, fstate: "N" }), frame({ slots: ["N", "R"], focus: 2, fstate: "R" })]);
  assert.equal(a.showing(t), false);
});

test("alert ignores the first frame", () => {
  const [a, t, fired] = after([frame({ slots: Array(6).fill("N") })]);
  assert.equal(fired, false);
  assert.equal(a.showing(t), false);
});

test("alert ignores the first frame after a reconnect", () => {
  const [a, t] = after([frame({ slots: ["R"] })]);
  a.update(null, "stale", false, t + 100);
  assert.equal(a.update(frame({ slots: ["N", "N"] }), "live", false, t + 2000), false);
  assert.equal(a.showing(t + 2000), false);
});

test("alert never fires during talk", () => {
  const [a, t, fired] = after([frame(), frame({ slots: ["N"] })], true);
  assert.equal(fired, false);
  assert.equal(a.showing(t), false);
  const [b, t2] = after([frame(), frame({ slots: ["N"] })]);
  b.update(frame({ slots: ["N"] }), "live", true, t2 + 50);
  assert.equal(b.showing(t2 + 200), false);
});

// --- Readout and actions ---------------------------------------------------------

test("wait formats like the pad", () => {
  assert.deepEqual([0, 42, 60, 305, 3600, 3900].map(L.wait), ["0s", "42s", "1m", "5m", "1h00", "1h05"]);
});

test("status line", () => {
  const f = frame({ slots: ["W", "N"], focus: 2, fstate: "N" });
  f.slots[1].since = 300;
  assert.equal(L.status(f, "live", 5.4), "a2 needs approval 5m");
  assert.equal(L.status(null, "none", 0), "connecting");
  assert.equal(L.status(f, "stale", 0), "tally offline");
  assert.equal(L.status(frame({ herdr: false }), "live", 0), "herdr offline");
  assert.equal(L.status(frame(), "live", 0), "no agents");
  assert.equal(L.status(frame({ slots: ["W"] }), "live", 0), "no agent focused");
  assert.equal(L.slotName({ ...frame(), focus: 0 }), "a+");
});

test("drive actions carry the target; fleet actions never do", () => {
  assert.equal(L.actionMessage(1, "approve_once", null), null);
  assert.deepEqual(L.actionMessage(1, "approve_once", "w1:p2"), { t: "action", id: 1, name: "approve_once", target: "w1:p2" });
  assert.deepEqual(L.actionMessage(2, "focus_slot", "w1:p2", { slot: 3 }), { t: "action", id: 2, name: "focus_slot", slot: 3 });
  assert.deepEqual(L.actionMessage(3, "next_agent", "w1:p2"), { t: "action", id: 3, name: "next_agent" });
  const long = L.actionMessage(4, "insert_text", "w1:p1", { text: "x".repeat(5000) });
  assert.equal(long.text.length, 4000);
});
