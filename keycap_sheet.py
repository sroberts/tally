"""Printable keycap legend sheet + reference card for the MacroPad (herdr layout).

Page 1: reference card (both layers, encoder, herdr config reminder).
Page 2: cut-out legend inserts at exact physical size, plus spares and blanks.

Usage: python3 keycap_sheet.py [insert_mm]   (default 12)
"""
import os
import sys
from reportlab.lib.pagesizes import letter
from reportlab.lib.units import mm
from reportlab.pdfgen import canvas
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.lib.colors import HexColor, white

INSERT = float(sys.argv[1]) if len(sys.argv) > 1 else 12.0
OUT = sys.argv[2] if len(sys.argv) > 2 else "macropad-keycap-sheet.pdf"

FONT_DIRS = [  # Linux, then macOS (brew install --cask font-dejavu)
    "/usr/share/fonts/truetype/dejavu",
    os.path.expanduser("~/Library/Fonts"),
    "/Library/Fonts",
]


def font(name):
    for d in FONT_DIRS:
        path = os.path.join(d, name)
        if os.path.exists(path):
            return path
    sys.exit(f"{name} not found in {FONT_DIRS}. On macOS: brew install --cask font-dejavu")


pdfmetrics.registerFont(TTFont("Sans", font("DejaVuSans.ttf")))
pdfmetrics.registerFont(TTFont("SansB", font("DejaVuSans-Bold.ttf")))
pdfmetrics.registerFont(TTFont("Cond", font("DejaVuSansCondensed.ttf")))
pdfmetrics.registerFont(TTFont("CondB", font("DejaVuSansCondensed-Bold.ttf")))
pdfmetrics.registerFont(TTFont("Mono", font("DejaVuSansMono.ttf")))

INK = HexColor("#16191d")
MUTED = HexColor("#5b636d")
RULE = HexColor("#c9ced4")
CUT = HexColor("#9aa1a9")
ROLE = {  # print-safe versions of the LED colors
    "RED": HexColor("#d7261e"),
    "BLUE": HexColor("#1f5fd6"),
    "AMBER": HexColor("#e07b00"),
    "GREEN": HexColor("#1e9a45"),
    "WHITE": HexColor("#8a9099"),
    "PURPLE": HexColor("#7a3fd1"),
}

# key: (label, role, action, sends)
DRIVE = [
    ("ESC", "RED", "Interrupt", "Esc"),
    ("MODE", "BLUE", "Cycle permission mode", "Shift+Tab"),
    ("REWIND", "BLUE", "Rewind menu", "Esc, Esc"),
    ("YES", "AMBER", "Approve once", "1"),
    ("ALWAYS", "AMBER", "Approve always", "2"),
    ("NO", "AMBER", "Deny", "3"),
    ("TAB", "WHITE", "Autocomplete", "Tab"),
    ("LOG", "WHITE", "Transcript view", "Ctrl+O"),
    ("COMPACT", "WHITE", "Compact context", "types /compact"),
    ("ENTER", "GREEN", "Submit", "Enter"),
    ("TALK", "PURPLE", "Speech to text (hold)", "holds Right Option"),
    ("BG", "WHITE", "Background task", "Ctrl+B, Ctrl+B"),
]
FLEET = [
    ("A1", "WHITE", "Focus agent 1", "pfx Shift+1"),
    ("A2", "WHITE", "Focus agent 2", "pfx Shift+2"),
    ("A3", "WHITE", "Focus agent 3", "pfx Shift+3"),
    ("A4", "WHITE", "Focus agent 4", "pfx Shift+4"),
    ("A5", "WHITE", "Focus agent 5", "pfx Shift+5"),
    ("A6", "WHITE", "Focus agent 6", "pfx Shift+6"),
    ("ATTN", "AMBER", "Agent needing attention", "pfx O"),
    ("NEW", "BLUE", "New Claude Code tab", "pfx Shift+C"),
    ("RESUME", "BLUE", "Resume in new tab", "pfx Shift+A"),
    ("ZOOM", "BLUE", "Zoom pane", "pfx Z"),
    ("LAST", "BLUE", "Last focused pane", "pfx ;"),
    ("ENTER", "GREEN", "Submit", "Enter"),
]
ENCODER = {
    "DRIVE": [("Turn right", "Down", "Down arrow"), ("Turn left", "Up", "Up arrow"), ("Press", "Next layer", "Fleet")],
    "FLEET": [("Turn right", "Next agent", "pfx ."), ("Turn left", "Previous agent", "pfx ,"), ("Press", "Next layer", "Drive")],
}

W, H = letter
M = 15 * mm  # page margin


def fit(c, text, font, size, maxw, minsize=4.5):
    while size > minsize and pdfmetrics.stringWidth(text, font, size) > maxw:
        size -= 0.25
    return size


def footer(c, page):
    c.setFont("Sans", 7)
    c.setFillColor(MUTED)
    c.drawString(M, 9 * mm, "MacroPad RP2040 · Claude Code on herdr · default prefix Ctrl+B (pfx)")
    c.drawRightString(W - M, 9 * mm, f"Page {page} of 2")


# ------------------------------------------------------------------ page 1
def pad_diagram(c, x, y, name, keys, enc, subtitle):
    """Draw one layer as the physical pad. (x, y) = top-left."""
    cell, gap = 26 * mm, 2.4 * mm
    gw = 3 * cell + 2 * gap
    c.setFillColor(INK)
    c.setFont("SansB", 13)
    c.drawString(x, y, name)
    c.setFont("Sans", 8)
    c.setFillColor(MUTED)
    c.drawString(x + pdfmetrics.stringWidth(name, "SansB", 13) + 3 * mm, y + 0.5, subtitle)

    # OLED + encoder strip
    top = y - 6 * mm
    c.setStrokeColor(RULE)
    c.setLineWidth(0.6)
    c.roundRect(x, top - 13 * mm, 2 * cell + gap, 13 * mm, 2 * mm, stroke=1, fill=0)
    c.setFont("Sans", 7)
    c.setFillColor(MUTED)
    c.drawString(x + 3 * mm, top - 5 * mm, "OLED shows layer name")
    c.drawString(x + 3 * mm, top - 9.5 * mm, "and key labels")
    ex = x + 2 * (cell + gap) + cell / 2
    c.circle(ex, top - 6.5 * mm, 6.5 * mm, stroke=1, fill=0)
    c.circle(ex, top - 6.5 * mm, 3.5 * mm, stroke=1, fill=0)

    gy = top - 17 * mm
    for i, (label, role, action, sends) in enumerate(keys):
        r, col = divmod(i, 3)
        kx = x + col * (cell + gap)
        ky = gy - r * (cell + gap)
        c.setStrokeColor(RULE)
        c.setFillColor(white)
        c.roundRect(kx, ky - cell, cell, cell, 2.2 * mm, stroke=1, fill=1)
        c.setFillColor(ROLE[role])
        c.roundRect(kx, ky - 3 * mm, cell, 3 * mm, 1.2 * mm, stroke=0, fill=1)
        c.rect(kx, ky - 3 * mm, cell, 1.5 * mm, stroke=0, fill=1)
        c.setFont("Mono", 6)
        c.setFillColor(MUTED)
        c.drawString(kx + 1.8 * mm, ky - 6.5 * mm, str(i))
        s = fit(c, label, "SansB", 11, cell - 4 * mm)
        c.setFont("SansB", s)
        c.setFillColor(INK)
        c.drawCentredString(kx + cell / 2, ky - 13.2 * mm, label)
        s = fit(c, action, "Cond", 7, cell - 3 * mm)
        c.setFont("Cond", s)
        c.setFillColor(INK)
        c.drawCentredString(kx + cell / 2, ky - 18.2 * mm, action)
        s = fit(c, sends, "Mono", 6.4, cell - 3 * mm)
        c.setFont("Mono", s)
        c.setFillColor(MUTED)
        c.drawCentredString(kx + cell / 2, ky - 22 * mm, sends)

    # encoder table
    ty = gy - 4 * (cell + gap) - 4 * mm
    c.setFont("SansB", 8)
    c.setFillColor(INK)
    c.drawString(x, ty, "Encoder")
    for j, (what, act, sends) in enumerate(enc):
        yy = ty - (j + 1) * 4.6 * mm
        c.setFont("Sans", 7.5)
        c.setFillColor(MUTED)
        c.drawString(x, yy, what)
        c.setFillColor(INK)
        c.drawString(x + 20 * mm, yy, act)
        c.setFont("Mono", 7)
        c.setFillColor(MUTED)
        c.drawRightString(x + gw, yy, sends)
        c.setStrokeColor(RULE)
        c.setLineWidth(0.4)
        c.line(x, yy - 1.6 * mm, x + gw, yy - 1.6 * mm)
    return gw


def page_reference(c):
    c.setFillColor(INK)
    c.setFont("SansB", 20)
    c.drawString(M, H - M - 4 * mm, "Claude Code Keypad")
    c.setFont("Sans", 9)
    c.setFillColor(MUTED)
    c.drawString(M, H - M - 10 * mm,
                 "Press the encoder to switch layers. Key numbers run 0-11 from the top-left, OLED at the top.")

    y0 = H - M - 22 * mm
    gw = pad_diagram(c, M, y0, "DRIVE", DRIVE, ENCODER["DRIVE"], "steer the focused session")
    pad_diagram(c, W - M - gw, y0, "FLEET", FLEET, ENCODER["FLEET"], "move between herdr agents")

    # legend + herdr notes
    by = 54 * mm
    c.setStrokeColor(RULE)
    c.setLineWidth(0.6)
    c.line(M, by + 8 * mm, W - M, by + 8 * mm)
    c.setFont("SansB", 8)
    c.setFillColor(INK)
    c.drawString(M, by + 2 * mm, "Key colors")
    lx = M
    for role, name in [("RED", "stop"), ("AMBER", "decision / attention"), ("BLUE", "mode and navigation"),
                       ("PURPLE", "voice"), ("GREEN", "submit"), ("WHITE", "utility")]:
        c.setFillColor(ROLE[role])
        c.roundRect(lx, by - 5.5 * mm, 3.2 * mm, 3.2 * mm, 0.6 * mm, stroke=0, fill=1)
        c.setFillColor(INK)
        c.setFont("Sans", 7.5)
        c.drawString(lx + 4.8 * mm, by - 4.8 * mm, name)
        lx += 4.8 * mm + pdfmetrics.stringWidth(name, "Sans", 7.5) + 7 * mm

    c.setFont("SansB", 8)
    c.setFillColor(INK)
    c.drawString(M, by - 14 * mm, "herdr setup")
    notes = [
        "pfx = herdr prefix, Ctrl+B by default. HERDR_PREFIX in code.py must match keys.prefix in herdr's config.toml.",
        "A1-A6, next/previous agent and LAST need the [keys] lines from MacroPad Layout Studio.",
        "NEW and RESUME need the studio's [[keys.command]] blocks, plus jq on the PATH.",
        "Agent numbers follow the order of the herdr sidebar. ATTN uses herdr's built-in notification jump and needs no config.",
        "Apply config changes with: herdr server reload-config",
    ]
    c.setFont("Sans", 7.5)
    for j, t in enumerate(notes):
        c.setFillColor(MUTED)
        c.drawString(M, by - (19.5 + j * 4.4) * mm, "·")
        c.setFillColor(INK)
        c.drawString(M + 3 * mm, by - (19.5 + j * 4.4) * mm, t)
    footer(c, 1)


# ------------------------------------------------------------------ page 2
def crop_marks(c, x, y, s, gap=1.2 * mm, length=2.2 * mm):
    """Corner crop marks around a square at (x, y) bottom-left, size s."""
    c.setStrokeColor(INK)
    c.setLineWidth(0.25)
    for cx, cy, dx, dy in [(x, y, -1, -1), (x + s, y, 1, -1), (x, y + s, -1, 1), (x + s, y + s, 1, 1)]:
        c.line(cx + dx * gap, cy, cx + dx * (gap + length), cy)
        c.line(cx, cy + dy * gap, cx, cy + dy * (gap + length))


def insert(c, x, y, s, main, role, sub=None, subrole=None):
    """One legend insert, bottom-left at (x, y), side s (points)."""
    c.setStrokeColor(CUT)
    c.setLineWidth(0.3)
    c.setDash(1, 1.4)
    c.rect(x, y, s, s, stroke=1, fill=0)
    c.setDash()
    crop_marks(c, x, y, s)
    band = s * 0.11
    c.setFillColor(ROLE[role])
    c.rect(x, y + s - band, s, band, stroke=0, fill=1)
    inner = s - 1.4 * mm
    if sub:
        size = fit(c, main, "CondB", s * 0.36, inner, 4)
        c.setFont("CondB", size)
        c.setFillColor(INK)
        c.drawCentredString(x + s / 2, y + s * 0.47, main)
        c.setStrokeColor(RULE)
        c.setLineWidth(0.3)
        c.line(x + s * 0.18, y + s * 0.34, x + s * 0.82, y + s * 0.34)
        ssize = fit(c, sub, "Cond", s * 0.2, inner * 0.8, 3.5)
        c.setFont("Cond", ssize)
        c.setFillColor(ROLE[subrole] if subrole not in ("WHITE",) else MUTED)
        c.drawCentredString(x + s / 2, y + s * 0.12, sub)
    else:
        size = fit(c, main, "CondB", s * 0.38, inner, 4)
        c.setFont("CondB", size)
        c.setFillColor(INK)
        c.drawCentredString(x + s / 2, y + s * 0.42 - size * 0.32 + s * 0.04, main)


def insert_block(c, x, ytop, title, note, items, s):
    """3x4 block of inserts matching the physical layout. Returns bottom y."""
    pitch = s + 8 * mm
    c.setFont("SansB", 10)
    c.setFillColor(INK)
    c.drawString(x, ytop, title)
    c.setFont("Sans", 7)
    c.setFillColor(MUTED)
    c.drawString(x, ytop - 4 * mm, note)
    gy = ytop - 9 * mm
    for i, it in enumerate(items):
        r, col = divmod(i, 3)
        ix = x + 3.5 * mm + col * pitch
        iy = gy - 3.5 * mm - s - r * pitch
        insert(c, ix, iy, s, *it)
    return gy - 4 * pitch


def page_inserts(c):
    s = INSERT * mm
    c.setFillColor(INK)
    c.setFont("SansB", 16)
    c.drawString(M, H - M - 3 * mm, f"Keycap inserts · {INSERT:g} mm")
    c.setFont("Sans", 8.5)
    c.setFillColor(MUTED)
    c.drawString(M, H - M - 9 * mm,
                 "Print at 100% (Actual size, no scaling). Check the 50 mm bar, then cut on the crop marks.")

    # calibration bar
    bx, byy = W - M - 50 * mm, H - M - 3 * mm
    c.setStrokeColor(INK)
    c.setLineWidth(0.6)
    c.line(bx, byy, bx + 50 * mm, byy)
    for t in range(0, 51, 10):
        c.line(bx + t * mm, byy, bx + t * mm, byy + (2.2 if t % 50 == 0 else 1.2) * mm)
    c.setFont("Sans", 6.5)
    c.setFillColor(INK)
    c.drawCentredString(bx + 25 * mm, byy - 3.5 * mm, "50 mm")

    col_w = 3 * (s + 8 * mm)
    top = H - M - 22 * mm
    dual = [(d[0], d[1], f[0], f[1]) for d, f in zip(DRIVE, FLEET)]
    right_x = M + col_w + 14 * mm

    insert_block(c, M, top, "Both layers", "Drive on top, Fleet below. Use these for one set of caps.",
                 dual, s)
    bottom = insert_block(c, right_x, top, "Drive only", "One legend per key.",
                          [(d[0], d[1]) for d in DRIVE], s)
    top2 = bottom - 10 * mm
    insert_block(c, M, top2, "Fleet only", "One legend per key.",
                 [(f[0], f[1]) for f in FLEET], s)

    # spare dual set (12 mm inserts are fiddly to cut) and blanks, right column
    bottom2 = insert_block(c, right_x, top2, "Spares", "A second Both-layers set for miscuts.", dual, s)

    # one row of blanks for handwritten legends
    by = bottom2 - 8 * mm
    c.setFont("SansB", 10)
    c.setFillColor(INK)
    c.drawString(M, by, "Blanks")
    c.setFont("Sans", 7)
    c.setFillColor(MUTED)
    c.drawString(M, by - 4 * mm, "Handwrite new legends as the layout changes.")
    pitch = s + 8 * mm
    n = int((W - 2 * M - 3.5 * mm) // pitch)
    for j in range(n):
        bxx = M + 3.5 * mm + j * pitch
        byy = by - 9 * mm - 3.5 * mm - s
        c.setStrokeColor(CUT)
        c.setLineWidth(0.3)
        c.setDash(1, 1.4)
        c.rect(bxx, byy, s, s, stroke=1, fill=0)
        c.setDash()
        crop_marks(c, bxx, byy, s)
    footer(c, 2)


c = canvas.Canvas(OUT, pagesize=letter)
c.setTitle("Claude Code Keypad legends")
c.setAuthor("Scott Roberts")
page_reference(c)
c.showPage()
page_inserts(c)
c.showPage()
c.save()
print(OUT)
