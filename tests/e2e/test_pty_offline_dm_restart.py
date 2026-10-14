import json
import os
import socket
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import pty_offline_dm_restart as runner


def connect_client(port: int) -> socket.socket:
    sock = socket.create_connection(("127.0.0.1", port), timeout=2)
    key = "dGhlIHNhbXBsZSBub25jZQ=="
    sock.sendall(
        (
            "GET / HTTP/1.1\r\n"
            "Host: 127.0.0.1\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            f"Sec-WebSocket-Key: {key}\r\n"
            "Sec-WebSocket-Version: 13\r\n\r\n"
        ).encode("ascii")
    )
    data = bytearray()
    while b"\r\n\r\n" not in data:
        data.extend(sock.recv(1024))
    accept = runner.ws_accept_value(key)
    if accept.encode("ascii") not in data or b" 101 " not in data:
        raise AssertionError("fixture handshake was rejected")
    return sock


def read_text(reader: runner.ByteReader) -> str:
    opcode, payload = reader.read_frame()
    if opcode != 1:
        raise AssertionError("fixture returned a non-text frame")
    return payload.decode("utf-8")


class PtyOfflineHarnessTests(unittest.TestCase):
    def test_fixture_relay_holds_ok_then_replays_the_same_event(self):
        port = runner.reserve_port()
        relay = runner.FixtureRelay(port)
        relay.start()
        try:
            sock = connect_client(port)
            event = {"id": "a" * 64, "pubkey": "b" * 64, "sig": "c" * 64, "content": "cipher", "kind": 30078, "tags": []}
            runner.write_frame(sock, 1, json.dumps(["EVENT", event]).encode(), mask=True)
            sock.settimeout(0.3)
            with self.assertRaises(socket.timeout):
                sock.recv(1)
            relay.release()
            sock.settimeout(2)
            ok = json.loads(read_text(runner.ByteReader(sock)))
            self.assertEqual(["OK", event["id"], True, ""], ok)
            query = connect_client(port)
            runner.write_frame(query, 1, json.dumps(["REQ", "hyphae-fetch", {}]).encode(), mask=True)
            query.settimeout(2)
            query_reader = runner.ByteReader(query)
            replay = json.loads(read_text(query_reader))
            eose = json.loads(read_text(query_reader))
            self.assertEqual("EVENT", replay[0])
            self.assertEqual("hyphae-fetch", replay[1])
            self.assertEqual(event["content"], replay[2]["content"])
            self.assertEqual(["EOSE", "hyphae-fetch"], eose)
            sock.close()
            query.close()
        finally:
            relay.close()

    def test_fixture_acks_alice_and_drops_bob_delivery(self):
        port = runner.reserve_port()
        relay = runner.FixtureRelay(port, drop_recipient=True)
        relay.start()
        try:
            sock = connect_client(port)
            event = {"id": "a" * 64, "pubkey": "b" * 64, "sig": "c" * 64, "content": "cipher", "kind": 30078, "tags": []}
            runner.write_frame(sock, 1, json.dumps(["EVENT", event]).encode(), mask=True)
            relay.release()
            sock.settimeout(2)
            ok = json.loads(read_text(runner.ByteReader(sock)))
            self.assertEqual(["OK", event["id"], True, ""], ok)
            self.assertEqual(event["content"], relay.events[0]["content"])
            query = connect_client(port)
            runner.write_frame(query, 1, json.dumps(["REQ", "hyphae-fetch", {}]).encode(), mask=True)
            query.settimeout(2)
            only = json.loads(read_text(runner.ByteReader(query)))
            self.assertEqual(["EOSE", "hyphae-fetch"], only)
            query.settimeout(0.3)
            with self.assertRaises(socket.timeout):
                query.recv(1)
            sock.close()
            query.close()
        finally:
            relay.close()

    def test_exit_requires_decrypted_recipient_marker(self):
        false_green = {
            "ok": True,
            "daemon_started": False,
            "same_event_on_reconnect": True,
            "relay_ack": True,
            "history_rows_before_quit": 1,
            "history_rows_before_ack": 1,
            "history_rows_after_ack": 1,
            "offline_exit": 0,
            "restart_exit": 0,
            "bob_exit": 0,
            "event_id_sha256": "ab" * 32,
            "signature_sha256": "cd" * 32,
            "ciphertext_sha256": "ef" * 32,
            "recipient": "timeout",
            "recipient_event_marker": "",
        }
        self.assertFalse(runner.harness_exit_ok([false_green], 1))
        missing_marker = dict(false_green, recipient="pass", recipient_event_marker="")
        self.assertFalse(runner.harness_exit_ok([missing_marker], 1))
        wrong_marker = dict(false_green, recipient="pass", recipient_event_marker="12" * 32)
        self.assertFalse(runner.harness_exit_ok([wrong_marker], 1))
        passed = dict(false_green, recipient="pass", recipient_event_marker=false_green["event_id_sha256"])
        self.assertTrue(runner.harness_exit_ok([passed], 1))
        self.assertFalse(runner.harness_exit_ok([passed, passed], 3))
        self.assertIsNone(runner.recipient_observation([], "body", "cipher", "e" * 64, ""))
        self.assertEqual(
            "pass",
            runner.recipient_observation(
                [{"id": "e" * 64, "content": "cipher", "plaintext": "body", "is_encrypted": True, "is_incoming": True}],
                "body",
                "cipher",
                "e" * 64,
                "body",
            ),
        )
        self.assertEqual(
            "fail",
            runner.recipient_observation(
                [{"id": "e" * 64, "content": "cipher", "plaintext": "body", "is_encrypted": True, "is_incoming": True}],
                "body",
                "cipher",
                "e" * 64,
                "body body",
            ),
        )

    def test_pty_child_enters_and_leaves_alt_screen_and_is_reaped(self):
        code = "import os; os.write(1, b'\\x1b[?1049hHI\\x1b[?1049l')"
        with tempfile.TemporaryDirectory() as temp_name:
            home = Path(temp_name)
            session = runner.PtySession([sys.executable, "-c", code], {"HOME": str(home), "PATH": "/usr/bin:/bin", "TERM": "xterm-256color"}, home)
            try:
                self.assertTrue(session.wait_exit(3))
                result = runner.alt_screen_result(bytes(session.raw))
                self.assertTrue(result["clean"])
                self.assertIn("HI", session.text())
            finally:
                code_exit = session.close()
            self.assertEqual(0, code_exit)
            self.assertIsNotNone(session.proc.poll())

    def test_existing_history_row_must_stay_singular(self):
        row = {"id": "e" * 64, "content": "cipher", "plaintext": "pty-dm-demo", "is_encrypted": True, "is_incoming": False}
        self.assertTrue(runner.rows_are_existing_plaintext([row], "pty-dm-demo", "cipher", "e" * 64))
        self.assertFalse(runner.rows_are_existing_plaintext([row, row], "pty-dm-demo", "cipher", "e" * 64))
        changed = dict(row, content="other")
        self.assertFalse(runner.rows_are_existing_plaintext([changed], "pty-dm-demo", "cipher", "e" * 64))

    def test_group_gate_is_unproven_and_sanitize_hides_secrets(self):
        gate = runner.group_runtime_gate()
        self.assertEqual("UNPROVEN", gate["status"])
        raw = "nsec1abcxyz /Users/jason/.hyphae/keystore.json npub1example pty-dm-secret"
        hidden = runner.sanitize(raw, ["/Users/jason/.hyphae/keystore.json", "pty-dm-secret"])
        self.assertNotIn("nsec1", hidden)
        self.assertNotIn("npub1", hidden)
        self.assertNotIn("pty-dm-secret", hidden)
        self.assertNotIn("/Users/jason", hidden)

    def test_receipt_rejects_private_material(self):
        with self.assertRaises(RuntimeError):
            runner.public_receipt({"body": "pty-dm-secret"}, [])
        safe = {"status": "pass", "group_runtime": "UNPROVEN", "source_commit": "a" * 40}
        self.assertEqual(safe, runner.public_receipt(safe, ["/Users/jason"]))


if __name__ == "__main__":
    unittest.main()
