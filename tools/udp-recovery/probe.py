"""Inert retained SOCKS UDP/STUN observation; never opens or replaces a socket."""
from __future__ import annotations

import ipaddress
import secrets
import socket
import struct
import time
from typing import Callable

STUN_TARGET = ("162.159.207.0", 3478)
WINDOW_STARTS = (0.0, 7.0, 27.0)
WINDOW_SECONDS = 3.0
MAX_REPLIES = 32


def association_identity(association) -> dict:
    """Capture exact borrowed socket objects and their current endpoints."""
    if association.udp.fileno() < 0 or association.control.fileno() < 0:
        raise RuntimeError("retained association is closed; replacement is forbidden")
    return {
        "udp_object": id(association.udp),
        "control_object": id(association.control),
        "udp_fd": association.udp.fileno(),
        "control_fd": association.control.fileno(),
        "udp_local": list(association.udp.getsockname()),
        "control_local": list(association.control.getsockname()),
        "control_peer": list(association.control.getpeername()),
        "relay": list(association.relay),
    }


def decode_reply(packet: bytes, relay_source: tuple, relay: tuple) -> bytes:
    if relay_source != relay:
        raise ValueError("reply came from a foreign SOCKS relay")
    if len(packet) < 10 or packet[:4] != b"\x00\x00\x00\x01":
        raise ValueError("reply is not an unfragmented numeric IPv4 SOCKS datagram")
    source = (socket.inet_ntoa(packet[4:8]), struct.unpack_from("!H", packet, 8)[0])
    if source != STUN_TARGET:
        raise ValueError("reply came from a foreign STUN endpoint")
    body = packet[10:]
    if len(body) < 20:
        raise ValueError("truncated STUN response")
    message_type, length, cookie = struct.unpack_from("!HHI", body)
    if message_type != 0x0101 or cookie != 0x2112A442 or length % 4 or len(body) != 20 + length:
        raise ValueError("malformed STUN binding success response")
    return body[8:20]


class RetainedSTUN:
    """One immutable association with a ledger spanning timeouts and windows."""

    def __init__(self, association, *, clock: Callable = time.monotonic):
        self.association = association
        self.clock = clock
        self.identity = association_identity(association)
        self.transactions: dict[str, dict] = {}
        self.received: list[dict] = []

    def assert_identity(self) -> None:
        if association_identity(self.association) != self.identity:
            raise RuntimeError("retained socket/control/endpoint identity changed")

    def probe(self, label: str, timeout: float = WINDOW_SECONDS, transaction_id: bytes | None = None) -> dict:
        if not 0 < timeout <= 5:
            raise ValueError("observation must be bounded to at most five seconds")
        self.assert_identity()
        transaction_id = secrets.token_bytes(12) if transaction_id is None else transaction_id
        if len(transaction_id) != 12 or transaction_id.hex() in self.transactions:
            raise ValueError("STUN transaction ID must be unique and exactly 96 bits")
        key = transaction_id.hex()
        start = self.clock()
        result = {"label": label, "transaction_id": key, "send_monotonic": start,
                  "timeout_seconds": timeout, "state": "unknown", "datagram_sent": False, "replies": []}
        self.transactions[key] = result
        payload = struct.pack("!HHI", 1, 0, 0x2112A442) + transaction_id
        packet = b"\x00\x00\x00\x01" + ipaddress.ip_address(STUN_TARGET[0]).packed + struct.pack("!H", STUN_TARGET[1]) + payload
        deadline = start + timeout
        stage = "send"
        try:
            self.association.udp.settimeout(timeout)
            sent = self.association.udp.sendto(packet, self.association.relay)
            if sent != len(packet):
                raise RuntimeError("short SOCKS UDP datagram send")
            result["datagram_sent"] = True
            result["datagram_sent_monotonic"] = self.clock()
            for _ in range(MAX_REPLIES):
                stage = "receive"
                remaining = deadline - self.clock()
                if remaining <= 0:
                    result["state"] = "timeout"
                    break
                self.association.udp.settimeout(remaining)
                try:
                    data, source = self.association.udp.recvfrom(2048)
                except (TimeoutError, socket.timeout):
                    result["state"] = "timeout"
                    break
                received_at = self.clock()
                stage = "decode"
                received_key = decode_reply(data, source, self.association.relay).hex()
                origin = self.transactions.get(received_key)
                item = {"transaction_id": received_key, "received_monotonic": received_at,
                        "observed_in": label, "source": list(STUN_TARGET),
                        "origin_label": origin["label"] if origin else None,
                        "kind": "current" if received_key == key else "prior" if origin else "unknown"}
                self.received.append(item)
                result["replies"].append(item)
                if received_key == key:
                    result["state"] = "response" if received_at <= deadline else "late_response"
                    result["response_monotonic"] = received_at
                    result["response_elapsed_seconds"] = received_at - start
                    result["received_by_deadline"] = received_at <= deadline
                    break
            else:
                stage = "ledger"
                raise RuntimeError("bounded reply ledger exhausted")
        except Exception as exc:
            result["state"] = "error"
            result["error"] = type(exc).__name__ + ": " + str(exc)
            result["error_stage"] = stage
            result["error_type"] = type(exc).__name__
        finally:
            result["end_monotonic"] = self.clock()
            self.assert_identity()
        return result

    def summary(self) -> dict:
        self.assert_identity()
        return {"identity": self.identity, "transactions": list(self.transactions.values()),
                "received": list(self.received)}
