# tally_link.py: non-blocking reader for Tally frames on the USB CDC data port.
# Newline-delimited JSON, protocol v2 (see the Tally design doc). The device
# answers ping with hello and keeps the last valid state frame. Nothing read
# here can trigger HID output.
import json

from render import LIVE, NONE, STALE

VERSION = 2
FIRMWARE = "0.1.0"
MAX_LINE = 512    # bytes, including the newline
STALE_AFTER = 15000  # ms without a state frame: three missed heartbeats
HELLO = b'{"t":"hello","v":2,"fw":"' + FIRMWARE.encode() + b'"}\n'


class Link:
    def __init__(self, stream):
        """stream: usb_cdc.data, or anything with in_waiting, read() and write()."""
        self.stream = stream
        try:  # never let a read or write wait on the host
            stream.timeout = 0
            stream.write_timeout = 0
        except AttributeError:
            pass
        self.buf = b""
        self.skipping = False  # inside an over-length line, waiting for its newline
        self.frame = None      # last valid state frame
        self.frame_time = None
        self.too_new = False   # host speaks a newer protocol: firmware needs updating
        self.hello()

    def hello(self):
        try:
            self.stream.write(HELLO)
        except OSError:
            pass  # host not reading yet; it will ping

    def poll(self, now):
        """Read whatever is waiting, never blocking. Call every loop.
        now: milliseconds, as for render.render."""
        waiting = self.stream.in_waiting
        if not waiting:
            return
        data = self.stream.read(waiting)
        if not data:
            return
        self.buf += data
        while True:
            end = self.buf.find(b"\n")
            if end < 0:
                if len(self.buf) >= MAX_LINE:
                    self.buf = b""
                    self.skipping = True
                return
            line = self.buf[:end]
            self.buf = self.buf[end + 1:]
            if self.skipping:
                self.skipping = False
                continue
            if end + 1 > MAX_LINE:
                continue
            self.handle(line, now)

    def handle(self, line, now):
        try:
            msg = json.loads(line.decode("utf-8"))
        except Exception:  # bad UTF-8 or JSON; ValueError on CircuitPython
            return
        if not isinstance(msg, dict):
            return
        t = msg.get("t")
        if t == "ping":
            self.hello()
        elif t == "state":
            v = msg.get("v")
            if not isinstance(v, int) or v > VERSION:
                self.too_new = isinstance(v, int)
                return
            self.too_new = False
            self.frame = msg
            self.frame_time = now
        # unknown types are ignored

    def status(self, now):
        if self.frame_time is None:
            return NONE
        if now - self.frame_time > STALE_AFTER:
            return STALE
        return LIVE
