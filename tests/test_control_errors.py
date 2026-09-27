"""Exercise the shipped _send method without requiring a Home Assistant installation.

Only the method is extracted, so these are unit regressions, not HA integration proof.
"""
import ast
import asyncio
from pathlib import Path
import unittest
from unittest.mock import AsyncMock, Mock

SOURCE = Path(__file__).parents[1] / 'custom_components/podconnect/media_player.py'


class SpotifyApiError(Exception):
    pass


class HomeAssistantError(Exception):
    pass


def load_send(name="_send"):
    tree = ast.parse(SOURCE.read_text())
    method = next(n for n in ast.walk(tree) if isinstance(n, ast.AsyncFunctionDef) and n.name == name)
    module = ast.Module(body=[method], type_ignores=[])
    namespace = dict(asyncio=asyncio, SpotifyApiError=SpotifyApiError, HomeAssistantError=HomeAssistantError)
    exec(compile(module, str(SOURCE), 'exec'), namespace)
    return namespace[name]


class ControlErrorTests(unittest.IsolatedAsyncioTestCase):
    async def test_rejections_propagate_and_clear_optimistic_state(self):
        for status in (401, 403, 404, 429, 500):
            with self.subTest(status=status):
                entity = Mock()
                entity._command_generation = 0
                entity.coordinator.poll_sequence = 0
                entity._optimistic_playing = True
                entity._optimistic_shuffle = True
                entity._optimistic_repeat = 'all'
                entity.coordinator.async_request_refresh = AsyncMock()
                action = AsyncMock(side_effect=SpotifyApiError(f'HTTP {status}'))
                with self.assertRaisesRegex(HomeAssistantError, str(status)):
                    await load_send()(entity, action())
                action.assert_awaited_once()
                entity.coordinator.async_request_refresh.assert_awaited_once()
                self.assertIsNone(entity._optimistic_playing)
                self.assertIsNone(entity._optimistic_shuffle)
                self.assertIsNone(entity._optimistic_repeat)
                entity.async_write_ha_state.assert_called_once()

    async def test_accepted_command_refreshes_once(self):
        entity = Mock()
        entity._command_generation = 0
        entity.coordinator.poll_sequence = 0
        entity._optimistic_playing = True
        entity.coordinator.async_request_refresh = AsyncMock()
        action = AsyncMock()
        await load_send()(entity, action())
        action.assert_awaited_once()
        entity.coordinator.async_request_refresh.assert_awaited_once()
        self.assertTrue(entity._optimistic_playing)


    async def test_library_errors_and_empty_results_do_not_play(self):
        for result in (SpotifyApiError('HTTP 403'), []):
            entity = Mock()
            entity._command_generation = 0
            entity.coordinator.poll_sequence = 0
            entity.coordinator.api.saved_tracks = AsyncMock()
            if isinstance(result, Exception):
                entity.coordinator.api.saved_tracks.side_effect = result
            else:
                entity.coordinator.api.saved_tracks.return_value = result
            entity._send = AsyncMock()
            with self.assertRaises(HomeAssistantError):
                await load_send('async_play_from_library')(entity, 'liked')
            entity._send.assert_not_awaited()

    async def test_search_errors_propagate(self):
        entity = Mock()
        entity._command_generation = 0
        entity.coordinator.poll_sequence = 0
        entity.coordinator.api.search = AsyncMock(side_effect=SpotifyApiError('HTTP 429'))
        with self.assertRaisesRegex(HomeAssistantError, '429'):
            await load_send('_search_top_uri')(entity, 'song', 'music')
        entity.coordinator.api.search.assert_awaited_once()

    async def test_no_search_match_does_not_play(self):
        entity = Mock()
        entity._command_generation = 0
        entity.coordinator.poll_sequence = 0
        entity._search_top_uri = AsyncMock(return_value=None)
        entity._send = AsyncMock()
        with self.assertRaisesRegex(HomeAssistantError, 'No Spotify match'):
            await load_send('async_play_media')(entity, 'music', 'missing song')
        entity._send.assert_not_awaited()

    async def test_missing_device_does_not_transfer(self):
        entity = Mock()
        entity._command_generation = 0
        entity.coordinator.poll_sequence = 0
        entity.coordinator.data = {'devices': []}
        entity._send = AsyncMock()
        with self.assertRaisesRegex(HomeAssistantError, 'unavailable'):
            await load_send('async_select_source')(entity, 'Kitchen')
        entity._send.assert_not_awaited()


if __name__ == '__main__':
    unittest.main()
