import json

import tally_link
from render import LIVE, NONE, STALE
from tally_link import HELLO, MAX_LINE, STALE_AFTER, Link


class FakeData:
    """Stands in for usb_cdc.data: bytes queued by the host, writes captured."""

    def __init__(self):
        self.incoming = b""
        self.written = []

    def feed(self, data):
        self.incoming += data

    @property
    def in_waiting(self):
        return len(self.incoming)

    def read(self, n):
        data, self.incoming = self.incoming[:n], self.incoming[n:]
        return data

    def write(self, data):
        self.written.append(data)


def state(**kw):
    f = {"t": "state", "v": 2, "seq": 1, "slots": ["W", None, None, None, None, None]}
    f.update(kw)
    return (json.dumps(f) + "\n").encode()


def test_hello_on_start_and_never_blocks():
    data = FakeData()
    Link(data)
    assert data.written == [HELLO]
    assert data.timeout == 0 and data.write_timeout == 0
    assert json.loads(HELLO) == {"t": "hello", "v": 2, "fw": tally_link.FIRMWARE}


def test_ping_gets_hello():
    data = FakeData()
    link = Link(data)
    data.feed(b'{"t":"ping"}\n')
    link.poll(0)
    assert data.written == [HELLO, HELLO]


def test_keeps_last_state_frame():
    data = FakeData()
    link = Link(data)
    data.feed(state(seq=1) + state(seq=2))
    link.poll(5)
    assert link.frame["seq"] == 2
    assert link.status(5) == LIVE


def test_line_split_across_polls():
    data = FakeData()
    link = Link(data)
    line = state(seq=7)
    data.feed(line[:10])
    link.poll(0)
    assert link.frame is None
    data.feed(line[10:])
    link.poll(1)
    assert link.frame["seq"] == 7


def test_status_none_until_first_frame_then_stale_after_15s():
    data = FakeData()
    link = Link(data)
    assert link.status(0) == NONE
    data.feed(state())
    link.poll(1000)
    assert link.status(1000 + STALE_AFTER) == LIVE
    assert link.status(1000 + STALE_AFTER + 1) == STALE
    data.feed(state())
    link.poll(1000 + STALE_AFTER + 2)
    assert link.status(1000 + STALE_AFTER + 2) == LIVE


def test_overlong_line_is_dropped_and_link_resyncs():
    data = FakeData()
    link = Link(data)
    data.feed(b'{"t":"state","v":2,"pad":"' + b"a" * MAX_LINE + b'"}\n' + state(seq=3))
    link.poll(0)
    assert link.frame["seq"] == 3


def test_overlong_line_across_polls_is_dropped():
    data = FakeData()
    link = Link(data)
    data.feed(b"x" * 300)
    link.poll(0)
    data.feed(b"x" * 300)
    link.poll(0)
    data.feed(b'","t":"state","v":2}\n' + state(seq=4))
    link.poll(0)
    assert link.frame["seq"] == 4


def test_line_at_exact_limit_is_accepted():
    data = FakeData()
    link = Link(data)
    head = b'{"t":"state","v":2,"p":"'
    pad = MAX_LINE - len(head) - len(b'"}\n')
    data.feed(head + b"a" * pad + b'"}\n')
    link.poll(0)
    assert link.frame is not None and len(link.frame["p"]) == pad


def test_garbage_is_ignored():
    data = FakeData()
    link = Link(data)
    data.feed(b"not json\n\xff\xfe\n[1,2]\n{\"t\":\"key\",\"n\":3}\n" + state(seq=9))
    link.poll(0)
    assert link.frame["seq"] == 9
    assert data.written == [HELLO]  # nothing triggers output except ping


def test_newer_protocol_is_ignored_and_flagged():
    data = FakeData()
    link = Link(data)
    data.feed(state(v=3))
    link.poll(0)
    assert link.frame is None
    assert link.too_new
    data.feed(state(v=2))
    link.poll(0)
    assert link.frame is not None
    assert not link.too_new
