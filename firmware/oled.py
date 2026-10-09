# oled.py: Tally status lines for the MacroPad's OLED (4 lines of 21
# characters in terminalio's 6 px font). lines() is pure, so CPython runs its
# tests on the host; StatusScreen imports displayio only when built.
from render import LIVE, NONE, SLOT_LABELS, STALE

WIDTH = 21
WORDS = {"N": "NEEDS APPROVAL", "G": "DONE", "W": "WORKING", "R": "READY", "U": "UNKNOWN"}


def _slot(focus):
    if focus == 0:
        return "+"  # an agent beyond the slot keys
    if isinstance(focus, int) and 1 <= focus <= len(SLOT_LABELS):
        return SLOT_LABELS[focus - 1]
    return ""


def _slots(frame):
    slots = frame.get("slots") if frame else None
    return slots if isinstance(slots, list) else []


def _overflow(frame, code=None):
    overflow = frame.get("overflow")
    if not isinstance(overflow, dict):
        return 0
    return sum(n for c, n in overflow.items() if isinstance(n, int) and (code is None or c == code))


def age(ms):
    """45s, 3m, 2h."""
    s = ms // 1000
    if s < 60:
        return str(s) + "s"
    if s < 3600:
        return str(s // 60) + "m"
    return str(s // 3600) + "h"


class Waits:
    """When each slot started waiting on you, as first seen by the pad.
    Call update() with every frame; a slot keeps its start time for as long
    as it stays blocked."""

    def __init__(self):
        self.frame = None
        self.since = []  # per slot: ms, or None when not blocked

    def update(self, frame, now):
        if frame is self.frame:
            return
        self.frame = frame
        since = []
        for i, code in enumerate(_slots(frame)):
            was = self.since[i] if i < len(self.since) else None
            since.append((now if was is None else was) if code == "N" else None)
        self.since = since

    def start(self, slot, default=None):
        """When slot (1-based) started waiting, or default."""
        if 1 <= slot <= len(self.since) and self.since[slot - 1] is not None:
            return self.since[slot - 1]
        return default

    def age(self, slot, now):
        """How long slot (1-based) has waited, as text, or ""."""
        start = self.start(slot)
        return "" if start is None else age(now - start)


def _fit(parts, sep="  "):
    """Join as many whole parts as fit on a line."""
    out = ""
    for part in parts:
        line = out + sep + part if out else part
        if len(line) > WIDTH:
            break
        out = line
    return out


def strip(frame):
    """Every slot at a glance: 1W 2N 3- ... and +N beyond the slots."""
    cells = [str(i + 1) + (c if isinstance(c, str) else "-") for i, c in enumerate(_slots(frame))]
    extra = _overflow(frame)
    return _fit([" ".join(cells)] + (["+" + str(extra)] if extra else []))


def _waiting(frame, waits, now):
    """Agents waiting on you other than the focused one, longest first."""
    focus = frame.get("focus")
    blocked = [i + 1 for i, c in enumerate(_slots(frame)) if c == "N" and i + 1 != focus]
    if waits:
        blocked.sort(key=lambda n: waits.start(n, now))
    named = [(SLOT_LABELS[n - 1] if n <= len(SLOT_LABELS) else str(n),
              waits.age(n, now) if waits else "") for n in blocked]
    parts = []
    if len(named) == 1:
        parts.append((named[0][0] + " WAITING " + named[0][1]).strip())
    elif named:
        parts.append(_fit(["WAIT"] + [(n + " " + a).strip() for n, a in named], " "))
    extra = _overflow(frame, "N") - (1 if focus == 0 and frame.get("fstate") == "N" else 0)
    if extra > 0:
        parts.append("+" + str(extra) + " WAIT")
    done = [str(i + 1) for i, c in enumerate(_slots(frame)) if c == "G" and i + 1 != focus]
    if done:
        parts.append("DONE " + ",".join(done))
    return _fit(parts)


def lines(layer, frame, status, too_new=False, waits=None, now=0):
    """The four status lines, or None when Tally has never sent a frame
    (keep the layer's label grid).

    layer: a code.py LAYERS entry; frame, status: as for render.render;
    too_new: the host speaks a newer protocol than this firmware;
    waits: a Waits updated with frame, for timers; now: milliseconds.
    """
    if status == NONE and not too_new:
        return None
    frame = frame or {}
    herdr = frame.get("herdr") is not False
    live = status == LIVE and herdr
    focus = frame.get("focus")
    slot = _slot(focus) if live else ""
    space = frame.get("space") if live else ""
    space = space if isinstance(space, str) else ""
    mark = "x" if status != LIVE else ("*" if herdr else "h")
    head = "{:<6}{:<3}{:<10} {}".format(layer["name"][:5], slot, space[:10], mark)

    summary = slots = ""
    if too_new:
        state = "UPDATE FW"
    elif status == STALE:
        state = "TALLY OFFLINE"
    elif not herdr:
        state = "HERDR OFFLINE"
    else:
        fstate = frame.get("fstate")
        if slot:
            state = slot + " " + WORDS.get(fstate, "UNKNOWN")
            if fstate == "N" and waits and isinstance(focus, int) and focus > 0:
                state = _fit([state, waits.age(focus, now)], " ")
        else:
            state = "NO AGENT FOCUSED"
        summary = _waiting(frame, waits, now)
        slots = strip(frame)

    return [line[:WIDTH] for line in (head, state, summary, slots)]


class StatusScreen:
    """Four label lines on the OLED; a label redraws only when its text
    changes."""

    def __init__(self, display):
        import displayio
        import terminalio
        from adafruit_display_text import label

        self.display = display
        self.group = displayio.Group()
        self.labels = []
        for i in range(4):
            line = label.Label(terminalio.FONT, text=" ", color=0xFFFFFF, x=0, y=7 + 16 * i)
            self.group.append(line)
            self.labels.append(line)
        self.text = [" "] * 4

    def update(self, text):
        for i, line in enumerate(text):
            line = line or " "
            if line != self.text[i]:
                self.labels[i].text = line
                self.text[i] = line

    def show(self):
        self.display.root_group = self.group
