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
AMBER = (255, 110, 0)
GREEN = (0, 200, 0)
BLUE = (0, 60, 255)
WHITE = (255, 255, 255)
OFF = (0, 0, 0)
PRESS = (255, 255, 255)

SLOT_LABELS = ("A1", "A2", "A3", "A4", "A5", "A6")
UNFOCUSED = 0.6   # slot keys other than the focused agent
DIMMED = 0.10     # every Fleet key while the link is stale or herdr is offline
QUIET = 0.10      # Fleet keys with no state of their own (NEW, RESUME, ...)


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
        return scale(WHITE, 0.08)
    if code == "U":
        return scale(WHITE, 0.03)
    return OFF


def _slots(frame):
    slots = frame.get("slots")
    return slots if isinstance(slots, list) else []


def _any_waiting(frame):
    if "N" in _slots(frame):
        return True
    overflow = frame.get("overflow")
    return isinstance(overflow, dict) and bool(overflow.get("N"))


def _fleet_key(label, base, frame, now):
    if label in SLOT_LABELS:
        n = SLOT_LABELS.index(label)
        slots = _slots(frame)
        color = slot_color(slots[n] if n < len(slots) else None, now)
        return color if frame.get("focus") == n + 1 else scale(color, UNFOCUSED)
    if label == "ATTN":
        if _any_waiting(frame):
            return AMBER if blink(now) else OFF
        return scale(AMBER, QUIET)
    if label == "ENTER":
        return scale(GREEN, 0.40) if frame.get("fstate") in ("R", "G") else OFF
    return scale(base, QUIET)


def render(layer, frame, status, held, now):
    """12 RGB tuples for the pad.

    layer: a code.py LAYERS entry, {"name": ..., "keys": [(label, color, action), ...]}
    frame: the last valid Tally state frame (dict), or None
    status: NONE, LIVE or STALE
    held: key numbers currently pressed
    now: milliseconds, time.monotonic_ns() // 1000000. Integer milliseconds
        because CircuitPython floats lose precision as uptime grows.
    """
    keys = layer["keys"]
    out = [key[1] for key in keys]  # static studio colors: the fallback

    if layer["name"] == "FLEET" and status != NONE and frame is not None:
        if status == STALE or frame.get("herdr") is False:
            out = [scale(key[1], DIMMED) for key in keys]
        else:
            out = [_fleet_key(key[0], key[1], frame, now) for key in keys]

    for n in held:
        if 0 <= n < len(out):
            out[n] = PRESS
    return out
