# render.py: Tally key lighting. Pure function, no hardware imports, so
# CPython runs its tests on the host.
#
# Keys are matched by OLED label rather than position, so rearranging a
# layout in MacroPad Layout Studio keeps the lighting on the right keys.
# Fleet slots are the keys labelled A1..A6.
import math

# Link status, from tally_link.Link.status()
NONE = "none"    # never received a frame: Tally isn't running
LIVE = "live"
STALE = "stale"  # no frame for 15 s

# Full-scale colors; render scales them by the percentages in the Tally doc.
RED = (255, 0, 0)
AMBER = (255, 110, 0)
GREEN = (0, 200, 0)
BLUE = (0, 60, 255)
PURPLE = (140, 0, 255)
WHITE = (255, 255, 255)
OFF = (0, 0, 0)
PRESS = (255, 255, 255)

SLOT_LABELS = ("A1", "A2", "A3", "A4", "A5", "A6")
UNFOCUSED = 0.6   # slot keys other than the focused agent
DIMMED = 0.10     # every Fleet key while the link is stale or herdr is offline
QUIET = 0.10      # keys with no state of their own (NEW, RESUME, MODE, ...)
FLASH = 100       # ms per phase of the cross-agent alert: on, off, on, off


def scale(color, pct):
    return (int(color[0] * pct), int(color[1] * pct), int(color[2] * pct))


def blink(now):
    """1 Hz on/off."""
    return now % 1000 < 500


def breathe(now):
    """2 s cycle between 15% and 35%."""
    return 0.25 - 0.10 * math.cos(2 * math.pi * (now % 2000) / 2000)


def slot_color(code, now):
    """Fleet slot color at full scale for a state code, or OFF."""
    if code == "N":
        return AMBER if blink(now) else OFF
    if code == "G":
        return GREEN
    if code == "W":
        return scale(BLUE, breathe(now))
    if code == "R":
        return scale(WHITE, 0.25)
    if code == "U":
        return scale(WHITE, 0.10)
    return OFF


def _slots(frame):
    slots = frame.get("slots")
    return slots if isinstance(slots, list) else []


def _waiting(frame):
    """How many agents are blocked, overflow included."""
    n = _slots(frame).count("N")
    overflow = frame.get("overflow")
    if isinstance(overflow, dict) and isinstance(overflow.get("N"), int):
        n += overflow["N"]
    return n


def _fleet_key(label, base, frame, now):
    if label in SLOT_LABELS:
        n = SLOT_LABELS.index(label)
        slots = _slots(frame)
        color = slot_color(slots[n] if n < len(slots) else None, now)
        return color if frame.get("focus") == n + 1 else scale(color, UNFOCUSED)
    if label == "ATTN":
        if _waiting(frame):
            return AMBER if blink(now) else OFF
        return scale(AMBER, QUIET)
    if label == "ENTER":
        return scale(GREEN, 0.40) if frame.get("fstate") in ("R", "G") else OFF
    return scale(base, QUIET)


def _drive_key(label, base, frame, now):
    """Drive keys light when they matter for the focused agent."""
    fstate = frame.get("fstate")
    if label == "ESC":
        return scale(RED, 0.30) if fstate in ("W", "N") else OFF
    if label in ("YES", "ALWAYS", "NO"):
        return AMBER if fstate == "N" and blink(now) else OFF
    if label == "ENTER":
        return scale(GREEN, 0.40) if fstate in ("R", "G") else OFF
    if label == "TALK":
        return scale(PURPLE, 0.15)
    if label == "BG":
        return scale(BLUE, 0.30) if fstate == "W" else OFF
    if label in ("TAB", "LOG", "COMPCT"):
        return scale(WHITE, 0.08)
    return scale(base, QUIET)


class Alert:
    """Cross-agent alert: the whole pad flashes amber twice when another
    agent starts waiting on you. Edge-triggered, one flash per new blocked
    agent, and never while TALK is held."""

    def __init__(self):
        self.frame = None
        self.waiting = None    # blocked agents in the last live frame; None: no baseline
        self.focused = False   # the focused agent was blocked
        self.start = None      # when the current flash began

    def update(self, frame, status, talk, now):
        """Call before each render. frame and status as for render; talk:
        TALK is held."""
        if talk:
            self.start = None
        if frame is self.frame:
            return
        self.frame = frame
        if frame is None or status != LIVE or frame.get("herdr") is False:
            self.waiting = None  # no flash for state seen on (re)connect
            return
        waiting, focused = _waiting(frame), frame.get("fstate") == "N"
        if self.waiting is not None and not talk:
            new = waiting - self.waiting
            if focused and not self.focused:
                new -= 1  # the agent in front of you: its keys already blink
            if new > 0:
                self.start = now
        self.waiting, self.focused = waiting, focused

    def flashing(self, now):
        if self.start is None:
            return False
        t = now - self.start
        if t >= 4 * FLASH:
            self.start = None
            return False
        return t // FLASH % 2 == 0


def render(layer, frame, status, held, now, flash=False):
    """12 RGB tuples for the pad.

    layer: a code.py LAYERS entry, {"name": ..., "keys": [(label, color, action), ...]}
    frame: the last valid Tally state frame (dict), or None
    status: NONE, LIVE or STALE
    held: key numbers currently pressed
    now: milliseconds, time.monotonic_ns() // 1000000. Integer milliseconds
        because CircuitPython floats lose precision as uptime grows.
    flash: Alert.flashing(now); every key shows amber
    """
    keys = layer["keys"]
    out = [key[1] for key in keys]  # static studio colors: the fallback

    live = status == LIVE and frame is not None and frame.get("herdr") is not False
    if layer["name"] == "FLEET" and status != NONE and frame is not None:
        if live:
            out = [_fleet_key(key[0], key[1], frame, now) for key in keys]
        else:
            out = [scale(key[1], DIMMED) for key in keys]
    elif layer["name"] == "DRIVE" and live:
        out = [_drive_key(key[0], key[1], frame, now) for key in keys]
    if flash:
        out = [AMBER] * len(out)

    for n in held:
        if 0 <= n < len(out):
            out[n] = PURPLE if keys[n][0] == "TALK" else PRESS
    return out
