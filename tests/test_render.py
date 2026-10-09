import pytest

import render
from render import AMBER, BLUE, GREEN, LIVE, NONE, OFF, PRESS, STALE, WHITE, scale

GREY = (60, 60, 60)  # code.py's WHITE

FLEET = {
    "name": "FLEET",
    "keys": [
        ("A1", GREY, None), ("A2", GREY, None), ("A3", GREY, None),
        ("A4", GREY, None), ("A5", GREY, None), ("A6", GREY, None),
        ("ATTN", AMBER, None), ("NEW", BLUE, None), ("RESUME", BLUE, None),
        ("ZOOM", BLUE, None), ("LAST", BLUE, None), ("ENTER", GREEN, None),
    ],
}
DRIVE = {
    "name": "DRIVE",
    "keys": [(label, (i, i, i), None) for i, label in enumerate(
        ["ESC", "MODE", "REWIND", "YES", "ALWAYS", "NO",
         "TAB", "LOG", "COMPCT", "ENTER", "TALK", "BG"])],
}

BLINK_ON = 100    # ms into a second: blink is on
BLINK_OFF = 600   # ms into a second: blink is off


def frame(**kw):
    f = {"t": "state", "v": 2, "slots": [None] * 6, "focus": None,
         "fstate": None, "overflow": {}, "herdr": True}
    f.update(kw)
    return f


def static(layer):
    return [k[1] for k in layer["keys"]]


def test_no_frame_keeps_static_colors():
    assert render.render(FLEET, None, NONE, set(), 0) == static(FLEET)
    assert render.render(DRIVE, None, NONE, set(), 0) == static(DRIVE)


def test_drive_layer_follows_the_focused_agent():
    quiet = [scale((1, 1, 1), 0.10), scale((2, 2, 2), 0.10)]  # MODE, REWIND: base x 10%
    white8, talk = scale(WHITE, 0.08), scale(render.PURPLE, 0.15)
    esc, enter, bg = scale(render.RED, 0.30), scale(GREEN, 0.40), scale(BLUE, 0.30)
    rows = {
        #       ESC  YES/ALWAYS/NO          ENTER  BG
        "N": [esc, [AMBER] * 3,             OFF,   OFF],
        "W": [esc, [OFF] * 3,               OFF,   bg],
        "G": [OFF, [OFF] * 3,               enter, OFF],
        "R": [OFF, [OFF] * 3,               enter, OFF],
        "U": [OFF, [OFF] * 3,               OFF,   OFF],
        None: [OFF, [OFF] * 3,              OFF,   OFF],
    }
    for fstate, (e, approve, ent, b) in rows.items():
        want = [e] + quiet + approve + [white8] * 3 + [ent, talk, b]
        got = render.render(DRIVE, frame(fstate=fstate), LIVE, set(), BLINK_ON)
        assert got == want, fstate


def test_slot_states_on_focused_key():
    for code, want in [("G", GREEN), ("R", scale(WHITE, 0.25)),
                       ("U", scale(WHITE, 0.10)), (None, OFF)]:
        f = frame(slots=[code] + [None] * 5, focus=1)
        assert render.render(FLEET, f, LIVE, set(), 0)[0] == want, code


def test_blocked_blinks_at_1hz():
    f = frame(slots=["N"] + [None] * 5, focus=1)
    assert render.render(FLEET, f, LIVE, set(), BLINK_ON)[0] == AMBER
    assert render.render(FLEET, f, LIVE, set(), BLINK_OFF)[0] == OFF
    assert render.render(FLEET, f, LIVE, set(), 1000 + BLINK_ON)[0] == AMBER


def test_working_breathes_between_15_and_35_percent():
    f = frame(slots=["W"] + [None] * 5, focus=1)
    blues = [render.render(FLEET, f, LIVE, set(), t)[0][2] for t in range(0, 2000, 50)]
    assert min(blues) == int(BLUE[2] * 0.15)
    assert max(blues) == pytest.approx(int(BLUE[2] * 0.35), abs=1)


def test_unfocused_slots_render_at_60_percent():
    f = frame(slots=["G", "G"] + [None] * 4, focus=2)
    out = render.render(FLEET, f, LIVE, set(), 0)
    assert out[0] == scale(GREEN, 0.6)
    assert out[1] == GREEN


def test_slots_follow_labels_not_positions():
    # A1 moved to the bottom-right key in the studio.
    keys = list(FLEET["keys"])
    keys[0], keys[11] = keys[11], keys[0]
    layer = {"name": "FLEET", "keys": keys}
    f = frame(slots=["G"] + [None] * 5, focus=1, fstate="G")
    out = render.render(layer, f, LIVE, set(), 0)
    assert out[11] == GREEN
    assert out[0] == scale(GREEN, 0.40)  # ENTER


def test_attn_blinks_when_any_agent_waits():
    quiet = render.render(FLEET, frame(), LIVE, set(), BLINK_ON)
    assert quiet[6] == scale(AMBER, 0.10)
    waiting = render.render(FLEET, frame(slots=[None, None, "N"] + [None] * 3), LIVE, set(), BLINK_ON)
    assert waiting[6] == AMBER
    assert render.render(FLEET, frame(slots=[None, None, "N"] + [None] * 3), LIVE, set(), BLINK_OFF)[6] == OFF


def test_attn_counts_overflow():
    out = render.render(FLEET, frame(overflow={"N": 1}), LIVE, set(), BLINK_ON)
    assert out[6] == AMBER


def test_enter_lights_when_focused_agent_is_ready_or_done():
    for fstate, want in [("R", scale(GREEN, 0.40)), ("G", scale(GREEN, 0.40)),
                         ("W", OFF), ("N", OFF), (None, OFF)]:
        assert render.render(FLEET, frame(fstate=fstate), LIVE, set(), 0)[11] == want, fstate


def test_other_fleet_keys_are_quiet():
    out = render.render(FLEET, frame(), LIVE, set(), 0)
    assert out[7:11] == [scale(BLUE, 0.10)] * 4


def test_stale_link_dims_fleet_to_10_percent():
    f = frame(slots=["N"] * 6, focus=1)
    assert render.render(FLEET, f, STALE, set(), BLINK_ON) == [scale(c, 0.10) for c in static(FLEET)]


def test_herdr_offline_dims_fleet_to_10_percent():
    f = frame(slots=["N"] * 6, herdr=False)
    assert render.render(FLEET, f, LIVE, set(), BLINK_ON) == [scale(c, 0.10) for c in static(FLEET)]


def test_held_keys_show_press_on_every_layer():
    assert render.render(DRIVE, None, NONE, {9}, 0)[9] == PRESS
    assert render.render(DRIVE, None, NONE, {10}, 0)[10] == render.PURPLE  # TALK
    assert render.render(FLEET, frame(slots=["N"] * 6), LIVE, {0, 5}, BLINK_OFF)[0] == PRESS


def test_bad_frame_fields_do_not_crash():
    f = {"t": "state", "v": 2, "slots": "junk", "overflow": [1], "focus": "x"}
    out = render.render(FLEET, f, LIVE, set(), 0)
    assert len(out) == 12
    assert render.render(FLEET, frame(slots=["N"]), LIVE, set(), 0)[5] == scale(OFF, 0.6)


def test_approval_keys_blink_when_focused_agent_waits():
    f = frame(fstate="N")
    assert render.render(DRIVE, f, LIVE, set(), BLINK_ON)[3:6] == [AMBER] * 3
    assert render.render(DRIVE, f, LIVE, set(), BLINK_OFF)[3:6] == [OFF] * 3


def test_drive_falls_back_to_static_colors_unless_live():
    f = frame(fstate="N")
    assert render.render(DRIVE, f, STALE, set(), BLINK_ON) == static(DRIVE)
    assert render.render(DRIVE, frame(fstate="N", herdr=False), LIVE, set(), BLINK_ON) == static(DRIVE)


def test_held_talk_is_purple_while_live():
    assert render.render(DRIVE, frame(fstate="W"), LIVE, {10}, 0)[10] == render.PURPLE


def test_flash_turns_every_key_amber_on_both_layers():
    for layer in (DRIVE, FLEET):
        assert render.render(layer, frame(fstate="W"), LIVE, set(), 0, flash=True) == [AMBER] * 12
    assert render.render(DRIVE, frame(), LIVE, {0}, 0, flash=True)[0] == PRESS


def alert_after(*frames, talk=False):
    """Feed frames to a fresh Alert 1 s apart; return it and the last time."""
    a, now = render.Alert(), 0
    for f in frames:
        now += 1000
        a.update(f, LIVE, talk, now)
    return a, now


def test_alert_flashes_twice_when_another_agent_starts_waiting():
    a, t = alert_after(frame(slots=["W", "R"], focus=1, fstate="W"),
                       frame(slots=["W", "N"], focus=1, fstate="W"))
    assert [a.flashing(t + ms) for ms in (0, 99, 100, 199, 200, 300)] == [True, True, False, False, True, False]
    assert not a.flashing(t + 400)
    assert not a.flashing(t + 1000)


def test_alert_counts_overflow():
    a, t = alert_after(frame(overflow={"R": 1}), frame(overflow={"N": 1}))
    assert a.flashing(t)


def test_alert_is_edge_triggered():
    waiting = frame(slots=["N", "R"], focus=2, fstate="R")
    a, t = alert_after(frame(slots=["R", "R"]), waiting)
    assert a.flashing(t)
    a.update(dict(waiting), LIVE, False, t + 5000)  # heartbeat, same state
    assert not a.flashing(t + 5000)


def test_alert_ignores_the_focused_agent():
    a, t = alert_after(frame(slots=["W"], focus=1, fstate="W"),
                       frame(slots=["N"], focus=1, fstate="N"))
    assert not a.flashing(t)


def test_alert_ignores_focus_moving_off_a_waiting_agent():
    a, t = alert_after(frame(slots=["N", "R"], focus=1, fstate="N"),
                       frame(slots=["N", "R"], focus=2, fstate="R"))
    assert not a.flashing(t)


def test_alert_ignores_the_first_frame():
    a, t = alert_after(frame(slots=["N"] * 6))
    assert not a.flashing(t)


def test_alert_never_fires_while_talk_is_held():
    a, t = alert_after(frame(), frame(slots=["N"]), talk=True)
    assert not a.flashing(t)
    a, t = alert_after(frame(), frame(slots=["N"]))
    a.update(a.frame, LIVE, True, t + 50)  # TALK pressed mid-flash
    assert not a.flashing(t + 200)
