"""Offline socket doubles: no host connection, process, session or fault."""
import socket
import struct
import unittest
from types import SimpleNamespace

from probe import RetainedSTUN, STUN_TARGET, decode_reply


class Clock:
    def __init__(self): self.now = 100.0
    def __call__(self): return self.now


class Sock:
    def __init__(self, clock, fd, local):
        self.clock, self.fd, self.local = clock, fd, local
        self.items, self.sent = [], []
    def fileno(self): return self.fd
    def getsockname(self): return self.local
    def getpeername(self): return ("127.0.0.1", 29370)
    def sendto(self, data, target):
        self.sent.append((data, target)); return len(data)
    def settimeout(self, seconds): self.timeout = seconds
    def recvfrom(self, limit):
        self.clock.now += 0.01
        if not self.items:
            self.clock.now += self.timeout
            raise socket.timeout()
        item = self.items.pop(0)
        if isinstance(item, Exception): raise item
        return item


def frame(tx, *, source=STUN_TARGET):
    return b"\x00\x00\x00\x01" + socket.inet_aton(source[0]) + struct.pack("!H", source[1]) + struct.pack("!HHI", 0x101, 0, 0x2112A442) + tx


class ProbeTests(unittest.TestCase):
    def setUp(self):
        self.clock = Clock()
        self.assoc = SimpleNamespace(udp=Sock(self.clock, 10, ("127.0.0.1", 53000)),
                                    control=Sock(self.clock, 11, ("127.0.0.1", 53001)),
                                    relay=("127.0.0.1", 54000))
        self.probe = RetainedSTUN(self.assoc, clock=self.clock)
    def queue(self, tx): self.assoc.udp.items.append((frame(tx), self.assoc.relay))
    def test_current_response_is_exact_transaction(self):
        tx=b"a"*12; self.queue(tx)
        result=self.probe.probe("prime", transaction_id=tx)
        self.assertEqual(result["state"], "response")
        self.assertEqual(result["replies"][0]["kind"], "current")
    def test_timeout_preserves_socket_and_transaction_ledger(self):
        identity=self.probe.identity.copy()
        result=self.probe.probe("first", transaction_id=b"a"*12)
        self.assertEqual(result["state"], "timeout")
        self.assertEqual(self.probe.summary()["identity"], identity)
        self.assertEqual(len(self.probe.transactions), 1)
    def test_prior_reply_cannot_satisfy_new_request(self):
        self.probe.probe("old", transaction_id=b"a"*12)
        self.queue(b"a"*12)
        result=self.probe.probe("new", transaction_id=b"b"*12)
        self.assertEqual(result["state"], "timeout")
        self.assertEqual(result["replies"][0]["origin_label"], "old")
        self.assertEqual(result["replies"][0]["kind"], "prior")
    def test_late_prior_and_new_reply_are_both_recorded(self):
        self.probe.probe("old", transaction_id=b"a"*12)
        self.queue(b"a"*12); self.queue(b"b"*12)
        result=self.probe.probe("new", transaction_id=b"b"*12)
        self.assertEqual(result["state"], "response")
        self.assertEqual([r["kind"] for r in result["replies"]], ["prior", "current"])
    def test_unknown_id_is_recorded_but_not_accepted(self):
        self.queue(b"z"*12)
        result=self.probe.probe("new", transaction_id=b"a"*12)
        self.assertEqual(result["state"], "timeout")
        self.assertEqual(result["replies"][0]["kind"], "unknown")
    def test_socket_replacement_is_refused_before_send(self):
        self.assoc.udp=Sock(self.clock, 10, ("127.0.0.1",53000))
        with self.assertRaisesRegex(RuntimeError, "identity changed"):
            self.probe.probe("bad")
        self.assertEqual(self.assoc.udp.sent, [])
    def test_control_or_relay_change_is_refused(self):
        self.assoc.relay=("127.0.0.1",54001)
        with self.assertRaises(RuntimeError): self.probe.probe("bad")
    def test_closed_control_is_refused(self):
        self.assoc.control.fd=-1
        with self.assertRaisesRegex(RuntimeError, "closed"):
            self.probe.probe("bad")
    def test_reused_transaction_is_refused(self):
        self.probe.probe("first", transaction_id=b"a"*12)
        with self.assertRaises(ValueError):
            self.probe.probe("second", transaction_id=b"a"*12)
    def test_malformed_response_is_unknown_error_not_timeout(self):
        self.assoc.udp.items.append((b"short",self.assoc.relay))
        result=self.probe.probe("bad")
        self.assertEqual(result["state"], "error")
        self.assertIn("ValueError",result["error"])
    def test_foreign_endpoint_and_relay_are_refused(self):
        with self.assertRaises(ValueError):
            decode_reply(frame(b"a"*12,source=("1.1.1.1",3478)),self.assoc.relay,self.assoc.relay)
        with self.assertRaises(ValueError):
            decode_reply(frame(b"a"*12),("127.0.0.1",9999),self.assoc.relay)
    def test_observation_and_reply_flood_are_bounded(self):
        with self.assertRaises(ValueError): self.probe.probe("bad", timeout=6)
        for _ in range(32): self.queue(b"z"*12)
        result=self.probe.probe("flood", transaction_id=b"a"*12)
        self.assertEqual(result["state"], "error")
        self.assertIn("ledger exhausted", result["error"])
    def test_reply_delivered_after_deadline_is_late_not_within_window(self):
        tx=b"a"*12;self.queue(tx)
        receive=self.assoc.udp.recvfrom
        def delayed(limit):
            self.clock.now+=4
            return receive(limit)
        self.assoc.udp.recvfrom=delayed
        result=self.probe.probe("delayed", transaction_id=tx)
        self.assertEqual(result["state"], "late_response")
        self.assertFalse(result["received_by_deadline"])
        self.assertGreater(result["response_elapsed_seconds"],3)
    def test_declared_stun_length_must_match_whole_datagram(self):
        with self.assertRaises(ValueError):
            decode_reply(frame(b"a"*12)+b"extra", self.assoc.relay,self.assoc.relay)


if __name__ == "__main__": unittest.main()
