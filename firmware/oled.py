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


def _numbers(slots, code):
    return ",".join(str(i + 1) for i, c in enumerate(slots) if c == code)


def hints(labels):
    """Key hints from a layer's labels: A1..A6 collapse to A1-6, then as
    many labels as fit."""
    words, slots = [], 0
    for label in labels:
        if label in SLOT_LABELS:
            slots += 1
            if slots > 1:
                continue
            label = "A1-6"
        if len(" ".join(words + [label])) > WIDTH:
            break
        words.append(label)
    return " ".join(words)


def lines(layer, frame, status, too_new=False):
    """The four status lines, or None when Tally has never sent a frame
    (keep the layer's label grid).

    layer: a code.py LAYERS entry; frame, status: as for render.render;
    too_new: the host speaks a newer protocol than this firmware.
    """
    if status == NONE and not too_new:
        return None
    frame = frame or {}
    herdr = frame.get("herdr") is not False
    focus = frame.get("focus")
    slot = _slot(focus) if status == LIVE and herdr else ""
    mark = "x" if status != LIVE else ("*" if herdr else "h")
    head = "{:<15}{:>3}  {}".format(layer["name"][:15], slot, mark)

    summary = ""
    if too_new:
        state = "UPDATE FW"
    elif status == STALE:
        state = "TALLY OFFLINE"
    elif not herdr:
        state = "HERDR OFFLINE"
    else:
        fstate = frame.get("fstate")
        state = (slot + " " + WORDS.get(fstate, "UNKNOWN")) if slot else "NO AGENT FOCUSED"
        slots = frame.get("slots")
        slots = slots if isinstance(slots, list) else []
        overflow = frame.get("overflow")
        extra = sum(n for n in overflow.values() if isinstance(n, int)) if isinstance(overflow, dict) else 0
        parts = []
        if "N" in slots:
            parts.append("WAIT " + _numbers(slots, "N"))
        if extra:
            parts.append("+" + str(extra))
        if "G" in slots:
            parts.append("DONE " + _numbers(slots, "G"))
        summary = "  ".join(parts)

    out = [head, state, summary, hints([k[0] for k in layer["keys"]])]
    return [line[:WIDTH] for line in out]


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
