// The browser pad: vibepad's one screen, talking protocol v3 to tally over
// the signed-in WebSocket at /ws. The page sends action names and pane
// targets, never keys; DRIVE actions carry the target on screen; actions
// are never retried.

import * as L from "/lighting.js";

// --- Keys --------------------------------------------------------------------

const drive = [
  { id: "esc", legend: "esc", name: "interrupt", action: "interrupt", key: "Escape" },
  { id: "mode", legend: "mode", name: "cycle mode", action: "cycle_mode", key: "m" },
  { id: "rewind", legend: "rewind", name: "rewind", action: "rewind", key: "r" },
  { id: "yes", legend: "yes", name: "approve once", action: "approve_once", key: "y" },
  { id: "always", legend: "always", name: "approve always", action: "approve_always", key: "a" },
  { id: "no", legend: "no", name: "deny", action: "deny", key: "n" },
  { id: "tab", legend: "tab", name: "autocomplete", action: "autocomplete", key: "Tab" },
  { id: "log", legend: "log", name: "transcript", action: "transcript", key: "o" },
  { id: "compact", legend: "compact", name: "compact", action: "compact", key: "c" },
  { id: "enter", legend: "enter", name: "submit", action: "submit", key: "Enter" },
  { id: "talk", legend: "talk", name: "talk", action: "insert_text" },
  { id: "bg", legend: "bg", name: "background", action: "background", key: "b" },
];

const tools = [
  { id: "new", legend: "new", name: "new claude", action: "new_claude", key: "N" },
  { id: "resume", legend: "resume", name: "resume claude", action: "resume_claude", key: "R" },
  { id: "zoom", legend: "zoom", name: "zoom", action: "zoom", key: "z" },
  { id: "last", legend: "last", name: "last pane", action: "last_pane", key: "l" },
  { id: "up", legend: "▲", name: "scroll up", action: "scroll_up", key: "ArrowUp", repeat: true },
  { id: "down", legend: "▼", name: "scroll down", action: "scroll_down", key: "ArrowDown", repeat: true },
];

const colors = { amber: "#fab413", blue: "#0071bb", gray: "#a1a7af", green: "#1e8a4c", red: "#b81d13" };
const inkOn = (hue) => (hue === "amber" || hue === "gray" ? "#0f0e12" : "#ffffff");

const pingStale = 8000; // tally sends state every 5 s; this long without a message is stale
const flashFor = 120;
const errorFor = 300;
const noticeFor = 3000;

// --- State -------------------------------------------------------------------

const s = {
  phase: "connecting", // connecting | live | stale | unreachable
  why: "",
  frame: null,
  frameAt: 0,
  hello: null,
  ws: null,
  nextID: 1,
  inFlight: new Map(), // id -> key
  flashes: new Map(), // key -> {ok, at}
  notice: null, // {text, until}
  beats: [],
  alert: new L.Alert(),
  alertAt: -Infinity,
  lastHeard: 0,
  backoff: 0,
  failures: 0,
  retry: null,
  talking: false,
  pressed: new Set(),
};

const $ = (id) => document.getElementById(id);
const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)");

function lighting() {
  if (!s.frame) return "none";
  return s.phase === "live" ? "live" : "stale";
}
const isLive = () => s.phase === "live";

// --- Connection ----------------------------------------------------------------

function connect() {
  clearTimeout(s.retry);
  if (s.ws || document.hidden) return;
  if (s.phase !== "live" && s.phase !== "stale") s.phase = "connecting";
  const ws = new WebSocket(`wss://${location.host}/ws`);
  s.ws = ws;
  let opened = false;
  ws.onopen = () => {
    opened = true;
    s.lastHeard = Date.now();
    s.nextID = 1;
    s.inFlight.clear();
  };
  ws.onmessage = (ev) => {
    s.lastHeard = Date.now();
    let m;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (m && typeof m === "object") handle(m);
  };
  ws.onclose = (ev) => {
    if (s.ws !== ws) return;
    s.ws = null;
    s.inFlight.clear();
    if (ev.code === 1008) return signedOut();
    drop(opened ? (ev.reason || "closed") : "can't reach tally", opened);
  };
}

// The connection ended: discard everything in flight, never retry it.
async function drop(why, wasOpen) {
  s.alert.update(s.frame, "stale", s.talking, Date.now());
  if (!wasOpen) {
    s.failures++;
    // A refused upgrade looks like any network failure; ask whether we're still signed in.
    try {
      const r = await fetch("/session", { cache: "no-store", credentials: "same-origin" });
      if (r.status === 401) return signedOut();
    } catch { /* offline */ }
  } else {
    s.failures = 0;
  }
  s.phase = s.failures >= 2 ? "unreachable" : "stale";
  s.why = why;
  schedule();
}

function schedule() {
  if (document.hidden) return;
  const steps = [250, 500, 1000, 2000, 4000, 8000];
  const wait = steps[Math.min(s.backoff, steps.length - 1)];
  s.backoff++;
  s.retry = setTimeout(connect, wait);
}

function signedOut() {
  location.replace("/login");
}

function suspend() {
  clearTimeout(s.retry);
  if (s.ws) {
    const ws = s.ws;
    s.ws = null;
    ws.close(1000, "paused");
  }
  s.inFlight.clear();
  s.phase = "connecting";
  s.alert.update(s.frame, "stale", s.talking, Date.now());
}

function handle(m) {
  if (s.phase !== "live" && (m.t === "hello" || m.t === "state")) {
    s.phase = "live";
    s.failures = 0;
  }
  switch (m.t) {
    case "hello":
      s.hello = m;
      s.backoff = 0;
      break;
    case "state": {
      s.backoff = 0;
      s.frame = L.parseFrame(m);
      s.frameAt = Date.now();
      beat();
      if (s.alert.update(s.frame, "live", s.talking, s.frameAt)) {
        s.alertAt = s.frameAt;
        navigator.vibrate?.([30, 60, 30]);
      }
      break;
    }
    case "ack": {
      const key = s.inFlight.get(m.id);
      if (key === undefined) return;
      s.inFlight.delete(m.id);
      s.flashes.set(key, { ok: m.ok === true, at: Date.now() });
      if (m.ok !== true) show(L.ackReason(m));
      break;
    }
  }
}

function beat() {
  s.beats.push(Date.now());
  if (s.beats.length > 24) s.beats.splice(0, s.beats.length - 24);
}

function show(text) {
  s.notice = { text, until: Date.now() + noticeFor };
}

// Stale: nothing heard for pingStale. Close and reconnect.
setInterval(() => {
  if (s.ws && s.ws.readyState === WebSocket.OPEN && Date.now() - s.lastHeard > pingStale) {
    const ws = s.ws;
    s.ws = null;
    ws.close();
    drop("no answer", true);
  }
}, 1000);

document.addEventListener("visibilitychange", () => {
  if (document.hidden) suspend();
  else { s.backoff = 0; connect(); }
  wake();
});
addEventListener("pageshow", (e) => { if (e.persisted) { s.backoff = 0; connect(); } });

// --- Actions -------------------------------------------------------------------

// Sends an action for the key the user tapped. It is written now or not at
// all: the page never queues across a reconnect and never resends.
function perform(action, key, opts = {}) {
  const fail = (text) => {
    s.flashes.set(key, { ok: false, at: Date.now() });
    show(text);
  };
  if (!isLive() || !s.ws || s.ws.readyState !== WebSocket.OPEN) return fail("not sent: offline");
  if (s.ws.bufferedAmount > 0) return fail("not sent: too late"); // a wedged socket; the 500 ms rule
  const msg = L.actionMessage(s.nextID, action, s.frame?.fpane, opts);
  if (!msg) return fail("no agent focused");
  s.inFlight.set(s.nextID, key);
  s.nextID++;
  s.ws.send(JSON.stringify(msg));
  navigator.vibrate?.(8);
}

// --- Build ---------------------------------------------------------------------

function el(tag, cls, html) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (html != null) e.innerHTML = html;
  return e;
}

function layers(e) {
  e.append(el("div", "g"), el("div", "f"));
  return e;
}

const tiles = [];
const keys = new Map(); // id -> {el, spec}

function build() {
  const fleet = $("fleet");
  const tileEl = (n) => {
    const b = layers(el("button", "face tile empty"));
    b.innerHTML += `<span class="in"><span class="head"><span class="name">a${n + 1}</span><span class="tw"></span></span><span class="word">—</span></span>`;
    b.setAttribute("aria-label", `agent ${n + 1}`);
    b.addEventListener("click", () => perform("focus_slot", `slot${n + 1}`, { slot: n + 1 }));
    tiles[n] = b;
    return b;
  };
  for (let n = 0; n < 3; n++) fleet.append(tileEl(n));
  const attn = layers(el("button", "face badge attn"));
  attn.innerHTML += `<span class="in"><span class="title">attn</span><span class="value">0</span></span>`;
  attn.setAttribute("aria-label", "attention");
  attn.addEventListener("click", () => perform("focus_attention", "attn"));
  fleet.append(attn);
  for (let n = 3; n < 6; n++) fleet.append(tileEl(n));
  const more = layers(el("div", "face badge more quiet"));
  more.innerHTML += `<span class="in"><span class="title">more</span><span class="value">+0</span></span>`;
  more.setAttribute("aria-label", "more agents");
  fleet.append(more);
  swipe(fleet);

  const grid = $("drive");
  drive.forEach((spec, i) => {
    const b = layers(el("button", "face key"));
    b.innerHTML += `<span class="idx">${String(i + 1).padStart(2, "0")}</span><span class="led"></span><span class="legend">${spec.legend}</span>`;
    b.setAttribute("aria-label", spec.name);
    if (spec.id === "talk") b.addEventListener("click", () => (s.talking ? closeTalk() : openTalk()));
    else b.addEventListener("click", () => perform(spec.action, spec.id));
    press(b, spec.id);
    grid.append(b);
    keys.set(spec.id, { el: b, spec });
  });

  const row = $("tools");
  for (const spec of tools) {
    const b = layers(el("button", "face key compact"));
    b.innerHTML += `<span class="legend">${spec.legend}</span>`;
    b.setAttribute("aria-label", spec.name);
    if (spec.repeat) repeat(b, spec);
    else {
      b.addEventListener("click", () => perform(spec.action, spec.id));
      press(b, spec.id);
    }
    row.append(b);
    keys.set(spec.id, { el: b, spec });
  }
}

// Long legends shrink to fit their key, down to 60%, as the app's do.
function fit() {
  for (const { el: b } of keys.values()) {
    const l = b.querySelector(".legend");
    l.style.fontSize = "";
    const full = parseFloat(getComputedStyle(l).fontSize);
    const room = b.clientWidth - 8;
    if (l.scrollWidth > room) l.style.fontSize = `${Math.max(full * 0.6, (full * room) / l.scrollWidth)}px`;
  }
}

// Pressed look while a finger is down.
function press(b, id) {
  const up = () => s.pressed.delete(id);
  b.addEventListener("pointerdown", () => s.pressed.add(id));
  for (const t of ["pointerup", "pointercancel", "pointerleave"]) b.addEventListener(t, up);
}

// ▲ and ▼ send once on touch-down, then repeat at 10 Hz while held.
function repeat(b, spec) {
  let timer = null, interval = null;
  const stop = () => {
    clearTimeout(timer);
    clearInterval(interval);
    timer = interval = null;
    s.pressed.delete(spec.id);
  };
  b.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    b.setPointerCapture?.(e.pointerId);
    s.pressed.add(spec.id);
    perform(spec.action, spec.id);
    timer = setTimeout(() => { interval = setInterval(() => perform(spec.action, spec.id), 100); }, 350);
  });
  for (const t of ["pointerup", "pointercancel", "lostpointercapture"]) b.addEventListener(t, stop);
  b.addEventListener("click", (e) => { if (e.detail === 0) perform(spec.action, spec.id); }); // keyboard activation
  b.addEventListener("contextmenu", (e) => e.preventDefault());
}

// A horizontal swipe across the strip moves to the next or previous agent.
function swipe(strip) {
  let x0 = 0, y0 = 0, on = false;
  strip.addEventListener("touchstart", (e) => {
    if (e.touches.length !== 1) return;
    on = true; x0 = e.touches[0].clientX; y0 = e.touches[0].clientY;
  }, { passive: true });
  strip.addEventListener("touchend", (e) => {
    if (!on) return;
    on = false;
    const t = e.changedTouches[0];
    const dx = t.clientX - x0, dy = t.clientY - y0;
    if (Math.abs(dx) > 48 && Math.abs(dx) > Math.abs(dy) * 1.5) perform(dx < 0 ? "next_agent" : "prev_agent", "swipe");
  }, { passive: true });
}

// --- Talk ----------------------------------------------------------------------

function openTalk() {
  if (!isLive()) return show("not sent: offline");
  s.talking = true;
  s.alert.cancel();
  s.alertAt = -Infinity;
  $("talk").hidden = false;
  const t = $("talk-text");
  t.value = "";
  updateCount();
  t.focus();
}

function closeTalk() {
  s.talking = false;
  $("talk").hidden = true;
  $("talk-text").blur();
}

function updateCount() {
  $("talk-count").textContent = `${[...$("talk-text").value].length}/${L.insertTextLimit}`;
}

function sendTalk() {
  const text = $("talk-text").value.trim();
  closeTalk();
  if (!text) return show("nothing to type");
  perform("insert_text", "talk", { text });
}

// --- Render --------------------------------------------------------------------

function flashOf(key, now) {
  const f = s.flashes.get(key);
  if (!f) return null;
  const age = now - f.at;
  if (f.ok) return age < flashFor ? "ok" : null;
  return age < errorFor ? "error" : null;
}

function paintLayers(e, glow, amount, flash) {
  const g = e.children[0], f = e.children[1];
  if (glow) { g.style.background = colors[glow.hue]; g.style.opacity = amount.toFixed(3); }
  else g.style.opacity = "0";
  if (flash) { f.style.background = flash === "ok" ? "#ffffff" : colors.red; f.style.opacity = "1"; }
  else f.style.opacity = "0";
}

function setText(e, text) {
  if (e.textContent !== text) e.textContent = text;
}

function render() {
  const now = Date.now();
  const f = s.frame;
  const link = lighting();
  const live = L.isLive(f, link);
  const rm = reduceMotion.matches;
  const elapsed = (now - s.frameAt) / 1000;
  const connected = isLive();

  // Header
  let [lc, lw] = ["rgb(229 229 229 / .5)", "link"];
  if (s.phase === "live") [lc, lw] = f?.herdr === false ? [colors.amber, "herdr off"] : ["#1e8a4c", "live"];
  else if (s.phase === "stale") [lc, lw] = [colors.amber, "stale"];
  else if (s.phase === "unreachable") [lc, lw] = ["#b81d13", "offline"];
  $("link").style.setProperty("--link-color", lc);
  setText($("link-word"), lw);
  setText($("host"), s.hello?.host ?? location.hostname.split(".")[0]);
  setText($("space"), live && f.space ? f.space : "");

  const slot = live ? L.slotName(f) : null;
  setText($("hero"), slot ?? "--");
  const hg = live && f.fstate ? L.slotGlow(f.fstate) : null;
  $("hero").style.setProperty("--hero-color", hg ? (hg.hue === "gray" ? "#e5e5e5" : colors[hg.hue]) : "rgb(229 229 229 / .5)");

  let words = L.status(f, link, elapsed);
  const fw = live ? L.focusedWait(f, elapsed) : null;
  if (slot && words.startsWith(slot + " ")) {
    words = words.slice(slot.length + 1);
    if (fw !== null) words = words.slice(0, -(L.wait(fw).length + 1));
  } else if (s.phase === "unreachable") words = "tally offline";
  setText($("state"), words);

  const detail = $("detail");
  if (s.notice && s.notice.until > now) {
    detail.classList.add("notice");
    setText(detail, s.notice.text);
  } else {
    detail.classList.remove("notice");
    setText(detail, detailText());
  }
  setText($("wait"), fw !== null ? L.wait(fw) : "");
  trace(now, connected);

  // Fleet
  const dimmed = L.stripDimmed(f, link) && f !== null;
  $("fleet").classList.toggle("dimmed", dimmed);
  const n = f ? f.slots.filter(Boolean).length + L.overflowTotal(f) : 0;
  setText($("fleet-trailing"), f ? (n === 1 ? "1 agent" : `${n} agents`) : "");
  tiles.forEach((t, i) => {
    const look = L.tile(i, f, link);
    const sl = f && i < f.slots.length ? f.slots[i] : null;
    const amount = look.glow ? L.intensity(look.glow, now, rm) * look.emphasis : 0;
    const flash = flashOf(`slot${i + 1}`, now);
    const hot = look.glow && amount > 0.45 && look.glow.hue !== "gray";
    const ink = flash === "ok" ? "#0f0e12" : flash === "error" ? "#ffffff" : hot ? inkOn(look.glow.hue) : sl ? "var(--ink)" : "var(--legend)";
    t.classList.toggle("empty", !sl);
    t.classList.toggle("focused", look.focused);
    t.style.setProperty("--tile-ink", ink);
    t.style.opacity = look.focused || !sl ? "1" : String(Math.max(look.emphasis, 0.75));
    t.disabled = !connected;
    paintLayers(t, look.glow, amount, flash);
    const w = L.slotWait(sl, elapsed);
    setText(t.querySelector(".tw"), w !== null ? L.wait(w) : "");
    setText(t.querySelector(".word"), sl ? L.shortWords[sl.s] : "—");
    t.setAttribute("aria-label", `agent ${i + 1}, ${sl ? L.shortWords[sl.s] : "empty"}${look.focused ? ", focused" : ""}`);
  });

  const attn = document.querySelector(".attn");
  const ag = L.attn(f, link);
  const aAmount = ag ? L.intensity(ag, now, rm) : 0;
  const aFlash = flashOf("attn", now);
  const aHot = ag && aAmount > 0.5;
  attn.style.setProperty("--badge-ink", aFlash === "ok" ? "#0f0e12" : aHot ? inkOn(ag.hue) : "var(--screen-ink)");
  paintLayers(attn, ag, aAmount, aFlash);
  const waiting = L.waiting(f);
  setText(attn.querySelector(".value"), String(waiting));
  attn.disabled = !connected;

  const more = document.querySelector(".more");
  const total = L.overflowTotal(f);
  const ow = f ? L.overflowWait(f, elapsed) : null;
  setText(more.querySelector(".title"), ow !== null ? L.wait(ow) : "more");
  setText(more.querySelector(".value"), `+${total}`);
  more.style.setProperty("--value-color", live && (f.overflow.N ?? 0) > 0 ? colors.amber : "inherit");
  more.classList.toggle("quiet", total === 0);

  // Drive and tools
  const target = live && f.fpane != null ? L.slotName(f) : null;
  setText($("drive-trailing"), target ? `target ${target}` : "no target");
  for (const { el: b, spec } of keys.values()) {
    const glow = L.drive(spec.action, f, link);
    const amount = glow ? L.intensity(glow, now, rm) : 0;
    const flash = flashOf(spec.id, now);
    const recording = spec.id === "talk" && s.talking;
    const lit = glow && amount > 0.5;
    b.classList.toggle("lit", !!glow);
    b.classList.toggle("recording", recording);
    b.classList.toggle("pressed", s.pressed.has(spec.id));
    b.classList.toggle("disabled", !connected);
    let ink = glow ? "var(--ink)" : "var(--legend)";
    if (lit) ink = inkOn(glow.hue);
    if (flash === "error" || recording) ink = "#ffffff";
    if (flash === "ok") ink = "#0f0e12";
    b.style.setProperty("--key-ink", ink);
    let led = "rgb(118 118 118 / .35)";
    if (recording) led = "#ffffff";
    else if (glow && amount > 0.05) led = lit ? inkOn(glow.hue) + "cc" : colors[glow.hue];
    b.style.setProperty("--led", led);
    paintLayers(b, recording ? null : glow, amount, flash);
    if (spec.id === "talk") setText(b.querySelector(".legend"), s.talking ? "cancel" : "talk");
  }

  // Alert border
  $("alert").hidden = !(now - s.alertAt < L.alertMS);

  if (!$("settings").hidden) renderSettings();
}

function via() {
  return location.hostname.endsWith(".ts.net") ? "tailscale" : location.hostname;
}

function detailText() {
  switch (s.phase) {
    case "live": return `via ${via()}` + (s.hello ? ` · tally ${s.hello.tally}` : "");
    case "connecting": return `connecting to ${location.hostname}`;
    case "stale": return s.why;
    default: return `can't reach ${location.hostname}`;
  }
}

// Every frame as a tick, scrolling left over ten seconds. Flat when nothing
// arrives, which is exactly what a stale link looks like.
function trace(now, live) {
  const c = $("trace");
  const dpr = devicePixelRatio || 1;
  const w = c.clientWidth, h = c.clientHeight;
  if (c.width !== Math.round(w * dpr) || c.height !== Math.round(h * dpr)) {
    c.width = Math.round(w * dpr);
    c.height = Math.round(h * dpr);
  }
  const ctx = c.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  const window = 10000;
  const t = reduceMotion.matches ? (s.beats.at(-1) ?? now) : now;
  ctx.fillStyle = "rgba(229,229,229,.18)";
  ctx.fillRect(0, h - 1, w, 1);
  for (const at of s.beats) {
    const age = t - at;
    if (age < 0 || age >= window) continue;
    const x = Math.round(w * (1 - age / window) * dpr) / dpr;
    ctx.globalAlpha = 1 - (age / window) * 0.7;
    ctx.fillStyle = live ? "#1e8a4c" : colors.amber;
    ctx.fillRect(x - 0.75, 0, 1.5, h);
  }
  ctx.globalAlpha = 1;
  ctx.fillStyle = live ? "rgba(229,229,229,.6)" : "rgba(229,229,229,.2)";
  ctx.fillRect(w - 2, 0, 2, h);
}

// --- Settings ------------------------------------------------------------------

function renderSettings() {
  setText($("s-host"), s.hello?.host ?? "-");
  setText($("s-version"), s.hello?.tally ?? "-");
  setText($("s-via"), via());
  setText($("s-link"), s.phase);
}

function store(key, fallback) {
  try { const v = localStorage.getItem(key); return v === null ? fallback : v === "1"; } catch { return fallback; }
}
function save(key, on) {
  try { localStorage.setItem(key, on ? "1" : "0"); } catch { /* private mode */ }
}

let lock = null;
async function wake() {
  const want = store("keepAwake", true) && !document.hidden && isLive();
  if (want && !lock && "wakeLock" in navigator) {
    try {
      lock = await navigator.wakeLock.request("screen");
      lock.addEventListener("release", () => { lock = null; });
    } catch { lock = null; }
  } else if (!want && lock) {
    lock.release();
    lock = null;
  }
}
setInterval(wake, 2000);

function settings() {
  const awake = $("s-awake");
  awake.checked = store("keepAwake", true);
  awake.addEventListener("change", () => { save("keepAwake", awake.checked); wake(); });
  if (!("wakeLock" in navigator)) $("s-awake-note").textContent = "this browser can't keep the screen on.";
  $("settings-open").addEventListener("click", () => { $("settings").hidden = false; renderSettings(); });
  $("settings-close").addEventListener("click", () => { $("settings").hidden = true; });
}

// --- Keyboard ------------------------------------------------------------------

function keyboard() {
  addEventListener("keydown", (e) => {
    if (!$("settings").hidden) {
      if (e.key === "Escape") $("settings").hidden = true;
      return;
    }
    if (s.talking) {
      if (e.key === "Escape") { e.preventDefault(); closeTalk(); }
      return;
    }
    if (e.metaKey || e.ctrlKey || e.altKey || e.repeat && !e.key.startsWith("Arrow")) return;
    if (/^[1-6]$/.test(e.key)) { e.preventDefault(); perform("focus_slot", `slot${e.key}`, { slot: Number(e.key) }); return; }
    if (e.key === "0") { e.preventDefault(); perform("focus_attention", "attn"); return; }
    if (e.key === "t") { e.preventDefault(); openTalk(); return; }
    for (const { spec } of keys.values()) {
      if (spec.key && spec.key === e.key) {
        e.preventDefault();
        perform(spec.action, spec.id);
        return;
      }
    }
  });
}

// --- Start ---------------------------------------------------------------------

function loop() {
  let last = 0;
  const tick = (t) => {
    if (t - last >= 33) { last = t; render(); }
    requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
}

build();
fit();
addEventListener("resize", fit);
document.fonts?.ready.then(fit);
settings();
keyboard();
$("talk-text").addEventListener("input", updateCount);
$("talk-text").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) { e.preventDefault(); sendTalk(); }
});
$("talk").addEventListener("submit", (e) => { e.preventDefault(); sendTalk(); });
$("talk-cancel").addEventListener("click", closeTalk);
document.addEventListener("touchstart", () => {}, { passive: true }); // :active on iOS
connect();
loop();
