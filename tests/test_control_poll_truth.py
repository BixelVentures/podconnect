"""Execute shipped coordination methods; unit boundaries, not a running HA instance."""

import ast
import asyncio
import time
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import AsyncMock, Mock

from test_control_errors import HomeAssistantError, SpotifyApiError

ROOT = Path(__file__).parents[1]


class Base:
    def _handle_coordinator_update(self):
        self.writes += 1


def entity():
    tree = ast.parse(
        (ROOT / "custom_components/podconnect/media_player.py").read_text()
    )
    methods = [
        n
        for n in ast.walk(tree)
        if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef))
        and n.name in ("_send", "_handle_coordinator_update")
    ]
    for method in methods:
        method.decorator_list = []
    cls = ast.ClassDef(
        name="Entity",
        bases=[ast.Name(id="Base", ctx=ast.Load())],
        keywords=[],
        body=methods,
        decorator_list=[],
    )
    module = ast.fix_missing_locations(ast.Module(body=[cls], type_ignores=[]))
    ns = {
        "asyncio": asyncio,
        "Base": Base,
        "SpotifyApiError": SpotifyApiError,
        "HomeAssistantError": HomeAssistantError,
    }
    exec(compile(module, "<shipped control methods>", "exec"), ns)  # noqa: S102 - Shipped methods only.
    obj = ns["Entity"]()
    obj.writes = 0
    obj._command_generation = 0
    obj._optimistic_poll_floor = None
    obj._optimistic_playing = True
    obj._optimistic_shuffle = True
    obj._optimistic_repeat = "all"
    obj.async_write_ha_state = Mock()
    obj.coordinator = SimpleNamespace(
        poll_sequence=4,
        last_update_success=True,
        data={"poll_sequence": 4, "playback": {"is_playing": False}},
        async_request_refresh=AsyncMock(),
    )
    return obj


class PollTruthTests(unittest.IsolatedAsyncioTestCase):
    async def test_fresh_contradictory_poll_replaces_accepted_intent(self):
        obj = entity()
        await obj._send(AsyncMock()())
        obj.coordinator.data["poll_sequence"] = 5
        obj._handle_coordinator_update()
        self.assertIsNone(obj._optimistic_playing)
        self.assertIsNone(obj._optimistic_shuffle)
        self.assertIsNone(obj._optimistic_repeat)

    async def test_precommand_inflight_poll_is_not_confirmation(self):
        obj = entity()
        await obj._send(AsyncMock()())
        obj._handle_coordinator_update()  # sequence4 started before acceptance
        self.assertTrue(obj._optimistic_playing)
        self.assertEqual(obj._optimistic_poll_floor, 4)

    async def test_failed_poll_is_not_confirmation(self):
        obj = entity()
        await obj._send(AsyncMock()())
        obj.coordinator.data["poll_sequence"] = 5
        obj.coordinator.last_update_success = False
        obj._handle_coordinator_update()
        self.assertTrue(obj._optimistic_playing)

    async def test_device_transfer_clears_old_intent(self):
        obj = entity()
        await obj._send(AsyncMock()())
        obj.coordinator.data = {"poll_sequence": 5, "playback": None}
        obj._handle_coordinator_update()
        self.assertIsNone(obj._optimistic_playing)

    async def test_old_command_completion_does_not_confirm_new_pending_command(self):
        obj = entity()
        first, second = asyncio.Event(), asyncio.Event()
        one = asyncio.create_task(obj._send(first.wait()))
        await asyncio.sleep(0)
        two = asyncio.create_task(obj._send(second.wait()))
        await asyncio.sleep(0)
        first.set()
        await one
        obj.coordinator.data["poll_sequence"] = 5
        obj._handle_coordinator_update()
        self.assertTrue(obj._optimistic_playing)
        self.assertIsNone(obj._optimistic_poll_floor)
        obj.coordinator.poll_sequence = 5
        second.set()
        await two
        self.assertEqual(obj._optimistic_poll_floor, 5)

    async def test_network_failure_drops_unconfirmed_intent(self):
        obj = entity()
        with self.assertRaises(ConnectionError):
            await obj._send(AsyncMock(side_effect=ConnectionError("offline"))())
        self.assertIsNone(obj._optimistic_playing)

    async def test_coordinator_sequence_is_allocated_before_fetch(self):
        tree = ast.parse(
            (ROOT / "custom_components/podconnect/coordinator.py").read_text()
        )
        method = next(
            n
            for n in ast.walk(tree)
            if isinstance(n, ast.AsyncFunctionDef) and n.name == "_async_update_data"
        )
        ns = {
            "asyncio": asyncio,
            "SpotifyApiError": SpotifyApiError,
            "UpdateFailed": RuntimeError,
            "Any": object,
            "dt_util": SimpleNamespace(utcnow=lambda: 0),
            "time": time,
        }
        owner_check = next(
            n
            for n in ast.walk(tree)
            if isinstance(n, ast.FunctionDef) and n.name == "_check_command_owner"
        )
        exec(  # noqa: S102 - Shipped coordinator methods only.
            compile(
                ast.Module(body=[owner_check, method], type_ignores=[]),
                "<shipped coordinator>",
                "exec",
            ),
            ns,
        )
        gate = asyncio.Event()

        async def playback():
            await gate.wait()
            return {"is_playing": False}

        obj = SimpleNamespace(
            poll_sequence=4,
            command_owner_guard=lambda: True,
            api=SimpleNamespace(
                playback_state=playback, devices=AsyncMock(return_value=[])
            ),
        )
        obj._check_command_owner = lambda: ns["_check_command_owner"](obj)
        task = asyncio.create_task(ns["_async_update_data"](obj))
        await asyncio.sleep(0)
        self.assertEqual(obj.poll_sequence, 5)
        gate.set()
        self.assertEqual((await task)["poll_sequence"], 5)

    async def test_cancelled_command_drops_unconfirmed_intent(self):
        obj = entity()
        gate = asyncio.Event()
        task = asyncio.create_task(obj._send(gate.wait()))
        await asyncio.sleep(0)
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        self.assertIsNone(obj._optimistic_playing)
        obj.coordinator.async_request_refresh.assert_not_awaited()


class PollUpdateFailed(Exception):
    pass


class CoordinatorReadDiagnosticTests(unittest.IsolatedAsyncioTestCase):
    def rig(self, playback=None, devices=None, logger=None):
        import hashlib

        from test_playback_response_truth import API

        tree = ast.parse(
            (ROOT / "custom_components/podconnect/coordinator.py").read_text()
        )
        methods = [
            node
            for node in ast.walk(tree)
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
            and node.name in {"_check_command_owner", "_async_update_data"}
        ]
        logger = logger or Mock()
        namespace = {
            "asyncio": asyncio,
            "hashlib": hashlib,
            "LOGGER": logger,
            "SpotifyApiError": API["SpotifyApiError"],
            "UpdateFailed": PollUpdateFailed,
            "Any": object,
            "time": time,
            "dt_util": SimpleNamespace(utcnow=lambda: 7),
        }
        exec(  # noqa: S102 - Execute only the existing shipped methods, as above.
            compile(
                ast.Module(body=methods, type_ignores=[]),
                "<shipped coordinator reads>",
                "exec",
            ),
            namespace,
        )
        obj = SimpleNamespace(
            poll_sequence=0,
            owner_epoch="PRIVATE-owner",
            command_owner_guard=lambda: True,
            api=SimpleNamespace(
                playback_state=playback or AsyncMock(return_value=None),
                devices=devices or AsyncMock(return_value=[]),
            ),
        )
        obj._check_command_owner = lambda: namespace["_check_command_owner"](obj)
        return obj, lambda: namespace["_async_update_data"](obj), logger, namespace

    def records(self, logger, name):
        return [
            call.args[0] % call.args[1:]
            for call in getattr(logger, name).call_args_list
        ]

    async def test_devices_only_failure_after_playback_success_and_one_recovery(self):
        import hashlib

        from test_playback_response_truth import API

        played = asyncio.Event()

        async def playback():
            played.set()
            return {"PRIVATE-track": "PRIVATE-content"}

        error = API["SpotifyApiError"]("PRIVATE-provider-body", status=503)

        async def devices():
            await played.wait()
            raise error

        obj, poll, logger, _ = self.rig(playback, devices)
        with self.assertRaises(PollUpdateFailed) as raised:
            await poll()
        self.assertEqual(
            str(raised.exception), "PRIVATE-provider-body"
        )  # Original legacy exception.
        self.assertIs(raised.exception.__cause__, error)
        expected_ref = hashlib.sha256(b"PRIVATE-owner:1").hexdigest()[:16]
        self.assertEqual(
            self.records(logger, "warning"),
            [
                f"playback_poll event=failed poll_ref={expected_ref} kind=spotify_api playback=success playback_kind=none playback_status=0 devices=error devices_kind=spotify_api devices_status=503"
            ],
        )
        self.assertFalse(logger.info.called)
        obj.api.devices = AsyncMock(return_value=[])
        self.assertEqual(
            (await poll())["playback"], {"PRIVATE-track": "PRIVATE-content"}
        )
        await poll()
        recovered_ref = hashlib.sha256(b"PRIVATE-owner:2").hexdigest()[:16]
        self.assertEqual(
            self.records(logger, "info"),
            [
                f"playback_poll event=recovered poll_ref={recovered_ref} previous_ref={expected_ref}"
            ],
        )
        self.assertEqual(len(self.records(logger, "warning")), 1)
        self.assertTrue(
            all(
                "PRIVATE" not in text
                for text in self.records(logger, "warning")
                + self.records(logger, "info")
            )
        )

    async def test_failure_record_follows_original_awaited_cancel_cleanup(self):
        from test_playback_response_truth import API

        entered, cleaned = asyncio.Event(), asyncio.Event()

        async def playback():
            entered.set()
            try:
                await asyncio.Event().wait()
            finally:
                await asyncio.sleep(0)
                cleaned.set()

        error = API["SpotifyApiError"]("original", status=429)

        async def devices():
            await entered.wait()
            raise error

        logger = Mock()
        logger.warning.side_effect = lambda *args: self.assertTrue(cleaned.is_set())
        _, poll, _, _ = self.rig(playback, devices, logger)
        with self.assertRaises(PollUpdateFailed):
            await poll()
        message = self.records(logger, "warning")[0]
        self.assertIn(
            "playback=cancelled playback_kind=none playback_status=0", message
        )
        self.assertIn(
            "devices=error devices_kind=spotify_api devices_status=429", message
        )
        self.assertEqual(logger.warning.call_count, 1)

    async def test_outer_cancellation_remains_cancelled_and_logs_after_cleanup(self):
        entered = [asyncio.Event(), asyncio.Event()]
        cleaned = [False, False]

        async def read(index):
            entered[index].set()
            try:
                await asyncio.Event().wait()
            finally:
                await asyncio.sleep(0)
                cleaned[index] = True

        logger = Mock()
        logger.warning.side_effect = lambda *args: self.assertTrue(all(cleaned))
        _, poll, _, _ = self.rig(lambda: read(0), lambda: read(1), logger)
        task = asyncio.create_task(poll())
        await asyncio.gather(*(event.wait() for event in entered))
        task.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await task
        message = self.records(logger, "warning")[0]
        self.assertIn("kind=outer_cancelled", message)
        self.assertIn("playback=cancelled", message)
        self.assertIn("devices=cancelled", message)
        self.assertEqual(logger.warning.call_count, 1)

    async def test_final_owner_guard_failure_is_not_recovery_even_two_reads_succeed(
        self,
    ):
        from test_playback_response_truth import API

        obj, poll, logger, _ = self.rig()
        obj.api.devices = AsyncMock(
            side_effect=API["SpotifyApiError"]("prior", status=500)
        )
        with self.assertRaises(PollUpdateFailed):
            await poll()
        obj.api.devices = AsyncMock(return_value=[])
        checks = iter(
            (True, True, True, False)
        )  # entry, playback, gather, final delivery.
        obj.command_owner_guard = lambda: next(checks)
        with self.assertRaisesRegex(PollUpdateFailed, "Observation owner replaced"):
            await poll()
        self.assertEqual(logger.info.call_count, 0)
        self.assertIn(
            "kind=owner_changed playback=success", self.records(logger, "warning")[-1]
        )
        self.assertIn("devices=success", self.records(logger, "warning")[-1])
        obj.command_owner_guard = lambda: True
        await poll()
        await poll()
        self.assertEqual(logger.info.call_count, 1)

    async def test_diagnostic_logger_and_hash_failures_preserve_original_results(self):
        from test_playback_response_truth import API

        for fault in ("logger", "logger_cancel", "hash"):
            with self.subTest(fault=fault):
                error = API["SpotifyApiError"]("legacy-original", status=503)
                obj, poll, logger, ns = self.rig(devices=AsyncMock(side_effect=error))
                if fault in {"logger", "logger_cancel"}:
                    sink_error = (
                        asyncio.CancelledError()
                        if fault == "logger_cancel"
                        else RuntimeError("sink failed")
                    )
                    logger.warning.side_effect = sink_error
                    logger.info.side_effect = sink_error
                else:
                    ns["hashlib"] = SimpleNamespace(
                        sha256=Mock(side_effect=RuntimeError("hash failed"))
                    )
                with self.assertRaises(PollUpdateFailed) as caught:
                    await poll()
                self.assertIs(caught.exception.__cause__, error)
                self.assertEqual(str(caught.exception), "legacy-original")
                obj.api.devices = AsyncMock(return_value=[])
                self.assertEqual((await poll())["devices"], [])
                await poll()
                self.assertLessEqual(logger.info.call_count, 1)

    async def test_status_only_exact_bounded_int_is_logged(self):
        from test_playback_response_truth import API

        for status in (True, "503", 99, 600, None, 100, 599):
            with self.subTest(status=status):
                _, poll, logger, _ = self.rig(
                    devices=AsyncMock(
                        side_effect=API["SpotifyApiError"]("PRIVATE", status=status)
                    )
                )
                with self.assertRaises(PollUpdateFailed):
                    await poll()
                expected = status if type(status) is int and 100 <= status <= 599 else 0
                self.assertIn(
                    f"devices_status={expected}", self.records(logger, "warning")[0]
                )

    async def test_api_error_status_metadata_preserves_message_and_response_reads(self):
        from test_playback_response_truth import API

        class Response:
            content_type = "text/plain"

            def __init__(self, status):
                self.headers = {"Retry-After": "17"}
                self.status, self.text_reads = status, 0

            async def __aenter__(self):
                return self

            async def __aexit__(self, *args):
                return False

            async def text(self):
                self.text_reads += 1
                return "original-body"

        for status in (401, 429, 503):
            with self.subTest(status=status):
                response = Response(status)
                api = API["SpotifyApi"].__new__(API["SpotifyApi"])
                api._session = SimpleNamespace(
                    async_ensure_token_valid=AsyncMock(),
                    token={"access_token": "inert"},
                )
                api._web = SimpleNamespace(request=Mock(return_value=response))
                with self.assertRaises(API["SpotifyApiError"]) as caught:
                    await api._request("GET", "/me/player")
                self.assertEqual(caught.exception.status, status)
                self.assertEqual(
                    str(caught.exception),
                    "rate limited (retry-after=17s)"
                    if status == 429
                    else f"GET /me/player -> {status}: original-body",
                )
                self.assertEqual(response.text_reads, 0 if status == 429 else 1)
                self.assertEqual(api._web.request.call_count, 1)

    async def test_failure_reference_uses_captured_owner_and_local_sequence(self):
        import hashlib

        from test_playback_response_truth import API

        entered, release = asyncio.Event(), asyncio.Event()

        async def devices():
            entered.set()
            await release.wait()
            raise API["SpotifyApiError"]("PRIVATE-content", status=503)

        obj, poll, logger, _ = self.rig(devices=devices)
        task = asyncio.create_task(poll())
        await entered.wait()
        obj.owner_epoch = "PRIVATE-replacement"
        obj.poll_sequence = 900
        release.set()
        with self.assertRaises(PollUpdateFailed):
            await task
        original_ref = hashlib.sha256(b"PRIVATE-owner:1").hexdigest()[:16]
        self.assertIn(f"poll_ref={original_ref}", self.records(logger, "warning")[0])
        self.assertNotIn("PRIVATE", self.records(logger, "warning")[0])
