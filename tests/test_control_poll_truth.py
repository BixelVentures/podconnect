"""Execute shipped coordination methods; unit boundaries, not a running HA instance."""
import ast
import asyncio
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock, Mock
from test_control_errors import SpotifyApiError, HomeAssistantError

ROOT = Path(__file__).parents[1]

class Base:
    def _handle_coordinator_update(self):
        self.writes += 1


def entity():
    tree = ast.parse((ROOT / 'custom_components/podconnect/media_player.py').read_text())
    methods = [n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in ('_send', '_handle_coordinator_update')]
    for method in methods:
        method.decorator_list = []
    cls = ast.ClassDef(name='Entity', bases=[ast.Name(id='Base', ctx=ast.Load())], keywords=[], body=methods, decorator_list=[])
    module = ast.fix_missing_locations(ast.Module(body=[cls], type_ignores=[]))
    ns = dict(asyncio=asyncio, Base=Base, SpotifyApiError=SpotifyApiError, HomeAssistantError=HomeAssistantError)
    exec(compile(module, '<shipped control methods>', 'exec'), ns)
    obj = ns['Entity']()
    obj.writes = 0
    obj._command_generation = 0
    obj._optimistic_poll_floor = None
    obj._optimistic_playing = True
    obj._optimistic_shuffle = True
    obj._optimistic_repeat = 'all'
    obj.async_write_ha_state = Mock()
    obj.coordinator = SimpleNamespace(poll_sequence=4, last_update_success=True, data={'poll_sequence':4, 'playback':{'is_playing':False}}, async_request_refresh=AsyncMock())
    return obj


class PollTruthTests(unittest.IsolatedAsyncioTestCase):
    async def test_fresh_contradictory_poll_replaces_accepted_intent(self):
        obj = entity()
        await obj._send(AsyncMock()())
        obj.coordinator.data['poll_sequence'] = 5
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
        obj.coordinator.data['poll_sequence'] = 5
        obj.coordinator.last_update_success = False
        obj._handle_coordinator_update()
        self.assertTrue(obj._optimistic_playing)

    async def test_device_transfer_clears_old_intent(self):
        obj = entity()
        await obj._send(AsyncMock()())
        obj.coordinator.data = {'poll_sequence':5, 'playback':None}
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
        obj.coordinator.data['poll_sequence'] = 5
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
            await obj._send(AsyncMock(side_effect=ConnectionError('offline'))())
        self.assertIsNone(obj._optimistic_playing)

    async def test_coordinator_sequence_is_allocated_before_fetch(self):
        tree = ast.parse((ROOT / 'custom_components/podconnect/coordinator.py').read_text())
        method = next(n for n in ast.walk(tree) if isinstance(n, ast.AsyncFunctionDef) and n.name == '_async_update_data')
        ns = {'SpotifyApiError':SpotifyApiError, 'UpdateFailed':RuntimeError, 'Any':object, 'dt_util':SimpleNamespace(utcnow=lambda:0)}
        exec(compile(ast.Module(body=[method], type_ignores=[]), '<shipped coordinator>', 'exec'), ns)
        gate = asyncio.Event()
        async def playback():
            await gate.wait()
            return {'is_playing':False}
        obj = SimpleNamespace(poll_sequence=4, api=SimpleNamespace(playback_state=playback, devices=AsyncMock(return_value=[])))
        task = asyncio.create_task(ns['_async_update_data'](obj))
        await asyncio.sleep(0)
        self.assertEqual(obj.poll_sequence, 5)
        gate.set()
        self.assertEqual((await task)['poll_sequence'], 5)

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
