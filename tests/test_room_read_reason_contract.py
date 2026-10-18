"""Actual read/client and extracted service serializer; no HA or network proof."""

import ast
import importlib.util
import json
import sys
import types
import unittest
from pathlib import Path

ROOT = Path(__file__).parents[1] / "custom_components/podconnect"
PKG = "inert_room_reason_contract"
package = types.ModuleType(PKG)
package.__path__ = [str(ROOT)]
sys.modules[PKG] = package


def load(name):
    spec = importlib.util.spec_from_file_location(
        PKG + "." + name, ROOT / (name + ".py")
    )
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


speakers = load("speakers")


class Rejected(Exception):
    pass


commands = types.ModuleType(PKG + ".commands")
commands.CommandRejected = Rejected
sys.modules[commands.__name__] = commands
observation = types.ModuleType(PKG + ".observations")
observation.current = lambda *args: None
sys.modules[observation.__name__] = observation
room = load("room_transport")


class Response:
    status = 409

    def __init__(self, body):
        self.body = body
        self.content = self

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def iter_chunked(self, size):
        for n in range(0, len(self.body), size):
            yield self.body[n : n + size]


class Client:
    def __init__(self, body):
        self.body = body
        self.calls = []

    def request(self, *args, **kwargs):
        self.calls.append((args, kwargs))
        return Response(self.body)


class ReadReasonContract(unittest.IsolatedAsyncioTestCase):
    async def invoke(self, body):
        client = Client(body)
        reader = speakers.SpeakersClient(client, "http://inert.invalid")
        data = types.SimpleNamespace(speakers=reader)
        owner = types.SimpleNamespace(
            data=data,
            controls=lambda *args: {"owner_epoch": "epoch", "poll_sequence": 7},
        )
        transport = room.RoomTransport(owner)
        transport.scope = lambda *args: {
            "account_user_id": "a",
            "target_id": "t",
            "room_id": "r",
            "homepod_id": "42",
        }
        data.commands = types.SimpleNamespace(room_transport=transport)
        tree = ast.parse((ROOT / "commands.py").read_text())
        method = next(
            n
            for n in ast.walk(tree)
            if isinstance(n, ast.AsyncFunctionDef) and n.name == "room_controls"
        )
        ns = {
            "__package__": PKG,
            "current": lambda *args: data,
            "hass": None,
            "CommandRejected": Rejected,
        }
        exec(  # noqa: S102 - Execute only the parsed, checked-in service method.
            compile(
                ast.Module(body=[method], type_ignores=[]),
                "actual-service-serializer",
                "exec",
            ),
            ns,
        )
        result = await ns["room_controls"](
            types.SimpleNamespace(
                data={
                    "config_entry_id": "a",
                    "target_id": "t",
                    "room_id": "r",
                    "homepod_id": "42",
                }
            )
        )
        self.assertEqual(len(client.calls), 1)
        self.assertEqual(client.calls[0][0][0], "GET")
        self.assertEqual(client.calls[0][1]["allow_redirects"], False)
        return result

    async def test_four_closed_reasons_through_client_read_serializer(self):
        for reason in (
            "claim_unavailable",
            "receiver_proof_unavailable",
            "output_proof_unavailable",
            "route_owner_unavailable",
        ):
            expected = {
                "ok": False,
                "error_kind": "room_transport_unavailable",
                "retry_allowed": False,
                "reason": reason,
            }
            self.assertEqual(await self.invoke(json.dumps(expected).encode()), expected)

    async def test_unknown_malformed_legacy_responses_stay_closed(self):
        good = {
            "ok": False,
            "error_kind": "room_transport_unavailable",
            "retry_allowed": False,
            "reason": "claim_unavailable",
        }
        bad = [
            dict(good, reason="SECRET"),
            dict(good, reason=[]),
            dict(good, reason=None),
            dict(good, extra="SECRET"),
            dict(good, retry_allowed=True),
            dict(good, ok=True),
            dict(good, error_kind="SECRET"),
        ]
        bodies = [json.dumps(x).encode() for x in bad] + [
            b"legacy room transport unavailable",
            b'{"reason":"claim_unavailable","reason":"SECRET"}',
            b"x" * 65537,
        ]
        for body in bodies:
            self.assertEqual(
                await self.invoke(body),
                {
                    "ok": False,
                    "error_kind": "room_transport_unavailable",
                    "retry_allowed": False,
                },
            )

    async def test_other_requests_do_not_parse_diagnostics(self):
        client = Client(
            json.dumps(
                {
                    "ok": False,
                    "error_kind": "room_transport_unavailable",
                    "retry_allowed": False,
                    "reason": "claim_unavailable",
                }
            ).encode()
        )
        reader = speakers.SpeakersClient(client, "http://inert.invalid")
        for method, path, flag in [
            ("GET", "/other", True),
            ("POST", "/api/room-transport/state?a=b", True),
            ("GET", "/api/room-transport/state?a=b", False),
        ]:
            with self.assertRaises(speakers.SpeakersError) as caught:
                await reader._request(method, path, room_read_reason=flag)
            self.assertIsNone(caught.exception.room_read_reason)
