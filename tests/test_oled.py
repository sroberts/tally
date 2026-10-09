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
    f = frame(slots=["W", "N", None, "G", "N", None], focus=2, fstate="N",
              overflow={"R": 1}, space="vibe-macro")
    waits = oled.Waits()
    waits.update({"slots": ["W", "N", None, "G", "R", None]}, 0)
    waits.update(f, 120000)
    assert oled.lines(FLEET, f, LIVE, waits=waits, now=300000) == [
        "FLEET A2 vibe-macro *",
        "A2 NEEDS APPROVAL 5m",
        "A5 WAITING 3m  DONE 4",
        "1W 2N 3- 4G 5N 6-  +1",
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
    beyond = oled.lines(DRIVE, frame(focus=0, fstate="N", overflow={"N": 2}), LIVE)
    assert beyond[0].startswith("DRIVE +  ") and beyond[1] == "+ NEEDS APPROVAL"
    assert beyond[2] == "+1 WAIT"  # the focused one is on line 2
    assert beyond[3].endswith("  +2")
    assert oled.lines(DRIVE, frame(), LIVE)[1] == "NO AGENT FOCUSED"


def test_link_indicator_and_offline_lines():
    f = frame(slots=["N"], focus=1, fstate="N")
    assert oled.lines(DRIVE, f, STALE)[:3] == ["DRIVE" + " " * 15 + "x", "TALLY OFFLINE", ""]
    assert oled.lines(DRIVE, frame(herdr=False), LIVE)[:3] == ["DRIVE" + " " * 15 + "h", "HERDR OFFLINE", ""]


def test_update_firmware():
    assert oled.lines(DRIVE, None, NONE, too_new=True)[1] == "UPDATE FW"


def test_no_tally_keeps_the_label_grid():
    assert oled.lines(FLEET, None, NONE) is None


def test_space_name_shows_only_while_live():
    f = frame(focus=1, fstate="R", slots=["R"], space="vibe")
    assert oled.lines(DRIVE, f, LIVE)[0] == "DRIVE A1 vibe       *"
    assert "vibe" not in oled.lines(DRIVE, f, STALE)[0]
    assert oled.lines(DRIVE, dict(f, space="a-very-long-space"), LIVE)[0] == "DRIVE A1 a-very-lon *"


def test_bad_frame_fields_do_not_crash():
    lines = oled.lines(FLEET, {"t": "state", "v": 2, "slots": "x", "overflow": [1], "focus": "x"}, LIVE)
    assert len(lines) == 4


def test_age():
    assert [oled.age(ms) for ms in (0, 59999, 60000, 3599999, 3600000)] == ["0s", "59s", "1m", "59m", "1h"]


def test_waits_keep_their_start_while_blocked():
    w = oled.Waits()
    w.update(frame(slots=["N", "R"]), 1000)
    w.update(frame(slots=["N", "N"]), 5000)
    assert w.age(1, 65000) == "1m" and w.age(2, 65000) == "1m"  # 64 s and 60 s
    assert w.age(1, 61000) == "1m" and w.age(2, 61000) == "56s"
    w.update(frame(slots=["R", "N"]), 70000)
    assert w.age(1, 70000) == "" and w.start(2) == 5000


def test_waiting_list_longest_first_without_the_focused_agent():
    w = oled.Waits()
    w.update(frame(slots=[None, None, "N"]), 0)
    f = frame(slots=["N", None, "N", "N"], focus=4, fstate="N")
    w.update(f, 30000)
    assert oled.lines(DRIVE, f, LIVE, waits=w, now=90000)[2] == "WAIT A3 1m A1 1m"


def test_waiting_without_timers():
    f = frame(slots=["N", "G", "G"], focus=3, fstate="G")
    assert oled.lines(DRIVE, f, LIVE)[2] == "A1 WAITING  DONE 2"


def test_slot_strip():
    assert oled.strip(frame(slots=["W", None, "U", "R", "G", "N"])) == "1W 2- 3U 4R 5G 6N"
    assert oled.strip(frame(overflow={"R": 2, "N": 1})) == "1- 2- 3- 4- 5- 6-  +3"
