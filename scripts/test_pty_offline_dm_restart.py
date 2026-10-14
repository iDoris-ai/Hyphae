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


def read_text(sock: socket.socket) -> str:
    reader = runner.ByteReader(sock)
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
            ok = json.loads(read_text(sock))
            self.assertEqual(["OK", event["id"], True, ""], ok)
            query = connect_client(port)
            runner.write_frame(query, 1, json.dumps(["REQ", "hyphae-fetch", {}]).encode(), mask=True)
            query.settimeout(2)
            replay = json.loads(read_text(query))
            eose = json.loads(read_text(query))
            self.assertEqual("EVENT", replay[0])
            self.assertEqual("hyphae-fetch", replay[1])
            self.assertEqual(event["content"], replay[2]["content"])
            self.assertEqual(["EOSE", "hyphae-fetch"], eose)
            sock.close()
            query.close()
        finally:
            relay.close()

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
