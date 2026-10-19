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


try:
    import aiohttp
except ModuleNotFoundError as exc:
    if exc.name != "aiohttp":
        raise
    aiohttp = None

if aiohttp is None:

    class ClientError(Exception):
        pass

    class ClientTimeout:
        def __init__(self, *, total):
            self.total = total

    aiohttp_stub = types.ModuleType("aiohttp")
    aiohttp_stub.ClientError = ClientError
    aiohttp_stub.ClientTimeout = ClientTimeout


class Rejected(Exception):
    pass


commands = types.ModuleType(PKG + ".commands")
commands.CommandRejected = Rejected
sys.modules[commands.__name__] = commands
observation = types.ModuleType(PKG + ".observations")
observation.current = lambda *args: None
sys.modules[observation.__name__] = observation
if aiohttp is None:
    _missing = object()
    _original_aiohttp = sys.modules.get("aiohttp", _missing)
    sys.modules["aiohttp"] = aiohttp_stub
try:
    speakers = load("speakers")
    room = load("room_transport")
finally:
    if aiohttp is None:
        if _original_aiohttp is _missing:
            del sys.modules["aiohttp"]
        else:
            sys.modules["aiohttp"] = _original_aiohttp


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

    async def test_content_claim_refusal_is_closed_and_get_only(self):
        good = {
            "ok": False,
            "error_kind": "native_account_claim_required",
            "retry_allowed": False,
        }
        for body, typed in [
            (good, True),
            ({**good, "extra": "private"}, False),
            ({**good, "ok": None}, False),
            ({**good, "retry_allowed": True}, False),
            ({**good, "error_kind": "private"}, False),
        ]:
            for method, path, enabled in [
                ("GET", "/api/room-content/state?a=b", True),
                ("POST", "/api/room-content/state?a=b", True),
                ("GET", "/other?a=b", True),
                ("GET", "/api/room-content/state?a=b", False),
            ]:
                client = Client(json.dumps(body).encode())
                reader = speakers.SpeakersClient(client, "http://inert.invalid")
                with self.assertRaises(speakers.SpeakersError) as caught:
                    if (
                        method == "GET"
                        and path == "/api/room-content/state?a=b"
                        and enabled
                    ):
                        await reader.room_content_state({"a": "b"})
                    else:
                        await reader._request(method, path, content_read_reason=enabled)
                self.assertEqual(
                    getattr(caught.exception, "content_read_reason", None),
                    "native_account_claim_required"
                    if typed
                    and method == "GET"
                    and path.startswith("/api/room-content/state?")
                    and enabled
                    else None,
                )
                self.assertEqual(len(client.calls), 1)

    async def test_content_actual_read_propagates_refusal_only_for_current_owner(self):
        content = load("room_content")
        body = {
            "ok": False,
            "error_kind": "native_account_claim_required",
            "retry_allowed": False,
        }
        for replaced in (False, True):
            reader = speakers.SpeakersClient(
                Client(json.dumps(body).encode()), "http://inert.invalid"
            )
            data = types.SimpleNamespace(
                speakers=reader,
                observations=types.SimpleNamespace(
                    playback=lambda entry: {
                        "ok": True,
                        "owner_epoch": "epoch",
                        "poll_sequence": 7,
                    }
                ),
            )
            adapter = content.RoomContent(types.SimpleNamespace(data=data))
            calls = []

            def owner(*args, calls=calls, replaced=replaced):
                calls.append(True)
                if replaced and len(calls) > 1:
                    raise Rejected("owner_replaced")
                return {
                    "account_user_id": "a",
                    "target_id": "t",
                    "room_id": "r",
                    "homepod_id": "42",
                }

            adapter._owner = owner
            with self.assertRaises(Rejected) as caught:
                await adapter.read(None, "a", "t", "r", "42")
            self.assertEqual(
                str(caught.exception),
                "owner_replaced" if replaced else "native_account_claim_required",
            )

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
