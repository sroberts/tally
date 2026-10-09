import oled
from render import LIVE, NONE, STALE

FLEET = {"name": "FLEET", "keys": [(label, None, None) for label in
         ["A1", "A2", "A3", "A4", "A5", "A6", "ATTN", "NEW", "RESUME", "ZOOM", "LAST", "ENTER"]]}
DRIVE = {"name": "DRIVE", "keys": [(label, None, None) for label in
         ["ESC", "MODE", "REWIND", "YES", "ALWAYS", "NO", "TAB", "LOG", "COMPCT", "ENTER", "TALK", "BG"]]}


def frame(**kw):
    f = {"t": "state", "v": 2, "slots": [None] * 6, "focus": None,
         "fstate": None, "overflow": {}, "herdr": True}
    f.update(kw)
    return f


def test_design_doc_example():
    f = frame(slots=["W", "N", None, "G", "N", None], focus=2, fstate="N", overflow={"R": 1})
    assert oled.lines(FLEET, f, LIVE) == [
        "FLEET           A2  *",
        "A2 NEEDS APPROVAL",
        "WAIT 2,5  +1  DONE 4",
        "A1-6 ATTN NEW RESUME",
    ]


def test_every_line_fits():
    f = frame(slots=["N", "G"] * 3, focus=0, fstate="N", overflow={"N": 30, "G": 9})
    for layer in (FLEET, DRIVE):
        for line in oled.lines(layer, f, LIVE):
            assert len(line) <= oled.WIDTH


def test_state_words():
    for code, word in [("N", "NEEDS APPROVAL"), ("G", "DONE"), ("W", "WORKING"),
                       ("R", "READY"), ("U", "UNKNOWN")]:
        assert oled.lines(DRIVE, frame(focus=3, fstate=code), LIVE)[1] == "A3 " + word


def test_focus_beyond_the_slots_and_on_a_shell():
    beyond = oled.lines(DRIVE, frame(focus=0, fstate="W", overflow={"W": 1}), LIVE)
    assert beyond[0].endswith("+  *") and beyond[1] == "+ WORKING" and beyond[2] == "+1"
    assert oled.lines(DRIVE, frame(), LIVE)[1] == "NO AGENT FOCUSED"


def test_link_indicator_and_offline_lines():
    f = frame(slots=["N"], focus=1, fstate="N")
    assert oled.lines(DRIVE, f, STALE)[:3] == ["DRIVE" + " " * 15 + "x", "TALLY OFFLINE", ""]
    assert oled.lines(DRIVE, frame(herdr=False), LIVE)[:3] == ["DRIVE" + " " * 15 + "h", "HERDR OFFLINE", ""]


def test_update_firmware():
    assert oled.lines(DRIVE, None, NONE, too_new=True)[1] == "UPDATE FW"


def test_no_tally_keeps_the_label_grid():
    assert oled.lines(FLEET, None, NONE) is None


def test_hints():
    assert oled.hints([k[0] for k in DRIVE["keys"]]) == "ESC MODE REWIND YES"
    assert oled.hints(["ZOOM", "A1", "A2"]) == "ZOOM A1-6"


def test_bad_frame_fields_do_not_crash():
    lines = oled.lines(FLEET, {"t": "state", "v": 2, "slots": "x", "overflow": [1], "focus": "x"}, LIVE)
    assert len(lines) == 4
