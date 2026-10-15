"""Real HA services and real OAuth HTTP client, exclusively inert loopback I/O."""

import asyncio
import copy
import importlib
import unittest
from unittest.mock import AsyncMock, patch

from aiohttp import web
from homeassistant.exceptions import HomeAssistantError
from homeassistant.helpers import entity_registry
import test_targets as fixture

observations = importlib.import_module('custom_components.podconnect.observations')


def track(uri='spotify:track:duplicate'):
    return {'type': 'track', 'uri': uri, 'name': 'Song', 'artists': [{'name': 'Artist'}],
            'duration_ms': 123000, 'access_token': 'NEVER_PUBLIC',
            'album': {'images': [{'url': 'https://i.scdn.co/image/ab67616d0000b2730123456789abcdef',
                                  'height': 640, 'width': 640, 'access_token': 'NEVER_PUBLIC'}]}}


def search_track(index):
    value = track(f"spotify:track:{index:022d}")
    value['type'] = 'track'
    return value


class ObservationFrameworkTests(unittest.IsolatedAsyncioTestCase):
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _writes = fixture.TargetFrameworkTests._writes

    async def asyncSetUp(self):
        self.queue_payload = {'currently_playing': track(), 'queue': [track(), track()]}
        self.queue_status = 200
        self.queue_entered = asyncio.Event()
        self.queue_release = asyncio.Event()
        self.queue_held = False
        self.queue_calls = 0
        self.playback_override = None
        self.search_payload = {'tracks': {'items': [search_track(index) for index in range(4)], 'total': 12}}
        self.search_queries = []
        self.search_entered = asyncio.Event()
        self.search_release = asyncio.Event()
        self.search_held = False
        await fixture.TargetFrameworkTests.asyncSetUp(self)
        self.holds.append(self.queue_release)
        self.holds.append(self.search_release)
        self.calls.clear()

    async def _backend(self, request):
        if request.path == '/spotify/search':
            self.assertEqual(request.method, 'GET')
            self.assertEqual(request.headers['Authorization'], 'Bearer inert-fixture-only')
            self.search_queries.append(dict(request.query))
            self.search_entered.set()
            if self.search_held:
                await self.search_release.wait()
            return web.json_response(self.search_payload)
        if request.path == '/spotify/me/player/queue':
            self.assertEqual(request.method, 'GET')
            self.assertEqual(request.headers['Authorization'], 'Bearer inert-fixture-only')
            self.calls.append((request.method, request.path, None, True))
            self.queue_calls += 1
            self.queue_entered.set()
            if self.queue_held:
                await self.queue_release.wait()
            if self.queue_status != 200:
                return web.Response(status=self.queue_status, text='Bearer NEVER_PUBLIC', headers={'Retry-After': '30'})
            return web.json_response(self.queue_payload)
        if request.path == '/spotify/me/player' and self.playback_override is not None:
            self.calls.append((request.method, request.path, None, True))
            return web.json_response(self.playback_override)
        return await fixture.TargetFrameworkTests._backend(self, request)

    async def service(self, name, account='account-alpha', **kwargs):
        return await self.hass.services.async_call('podconnect', name,
            {'config_entry_id': account, **kwargs}, blocking=True, return_response=True)

    async def test_library_reads_exact_account_and_preserves_single_account_omission(self):
        alpha, beta = self.entries
        alpha_read = AsyncMock(return_value=[track('spotify:track:alpha')])
        beta_read = AsyncMock(return_value=[track('spotify:track:beta')])
        with patch.object(alpha.runtime_data.api, 'top_tracks', alpha_read), \
             patch.object(beta.runtime_data.api, 'top_tracks', beta_read):
            selected = await self.hass.services.async_call('podconnect', 'top_tracks',
                {'config_entry_id':'account-beta'}, blocking=True, return_response=True)
            self.assertEqual(selected['config_entry_id'], 'account-beta')
            self.assertEqual(selected['tracks'][0]['uri'], 'spotify:track:beta')
            self.assertEqual(selected['tracks'][0]['artwork'], {
                'kind':'album_cover', 'provider':'spotify',
                'url':'https://i.scdn.co/image/ab67616d0000b2730123456789abcdef'})
            beta_read.assert_awaited_once()
            alpha_read.assert_not_awaited()
            with self.assertRaises(HomeAssistantError):
                await self.hass.services.async_call('podconnect', 'top_tracks', {},
                    blocking=True, return_response=True)

            self.assertTrue(await self.hass.config_entries.async_unload(beta.entry_id))
            legacy = await self.hass.services.async_call('podconnect', 'top_tracks', {},
                blocking=True, return_response=True)
            self.assertEqual(legacy['config_entry_id'], 'account-alpha')
            self.assertEqual(legacy['tracks'][0]['uri'], 'spotify:track:alpha')
            alpha_read.assert_awaited_once()

    async def test_library_read_replacement_during_await_cannot_publish(self):
        alpha = self.entries[0]
        original = alpha.runtime_data
        entered, release = asyncio.Event(), asyncio.Event()

        async def held_read():
            entered.set()
            await release.wait()
            return [track('spotify:track:stale')]

        with patch.object(original.api, 'top_tracks', held_read):
            call = self._task(self.hass.services.async_call('podconnect', 'top_tracks',
                {'config_entry_id':alpha.entry_id}, blocking=True, return_response=True))
            await asyncio.wait_for(entered.wait(), 2)
            alpha.runtime_data = copy.copy(original)
            release.set()
            with self.assertRaises(HomeAssistantError):
                await call
            alpha.runtime_data = original

    async def test_search_uses_exact_account_actual_query_and_closed_sample(self):
        alpha, beta = self.entries
        with patch.object(alpha.runtime_data.api, 'search', side_effect=AssertionError('wrong account')):
            result = await self.hass.services.async_call('podconnect', 'search_tracks',
                {'config_entry_id':beta.entry_id,'query':'  jazz guitar  ','limit':2},
                blocking=True, return_response=True)
        self.assertEqual(result['config_entry_id'], beta.entry_id)
        self.assertEqual(result['query'], 'jazz guitar')
        self.assertEqual(result['source'], 'spotify_web_api')
        self.assertEqual(result['coverage'], 'spotify_search_sample')
        self.assertEqual((result['source_sample_count'],result['returned_count'],result['source_has_more']), (2,2,True))
        self.assertEqual(result['tracks'][0]['uri'], 'spotify:track:0000000000000000000000')
        self.assertEqual(result['tracks'][0]['artwork']['provider'], 'spotify')
        self.assertNotIn('NEVER_PUBLIC', str(result))
        self.assertEqual(self.search_queries[-1], {'q':'jazz guitar','type':'track','limit':'2'})
        self.assertEqual(self._writes(), [])

    async def test_search_schema_bounds_owner_and_query_rejections_do_not_call_spotify(self):
        for body in (
            {'query':'song'},
            {'config_entry_id':'account-alpha','query':' '},
            {'config_entry_id':'account-alpha','query':'x'*161},
            {'config_entry_id':'account-alpha','query':7},
            {'config_entry_id':'account-alpha','query':'song','limit':True},
            {'config_entry_id':'account-alpha','query':'song','limit':0},
            {'config_entry_id':'account-alpha','query':'song','limit':21},
            {'config_entry_id':'account-alpha','query':'song','limit':'3'},
        ):
            response = await self.client.post('/api/services/podconnect/search_tracks?return_response', json=body)
            self.assertEqual(response.status, 400, body)
        with self.assertRaises(HomeAssistantError):
            await self.service('search_tracks', 'unknown', query='song')
        self.assertEqual(self.search_queries, [])
        self.assertEqual(self._writes(), [])

    async def test_search_result_bounds_wrong_types_and_actual_empty_sample(self):
        self.search_payload = {'tracks':{'items':[],'total':0}}
        empty = await self.service('search_tracks', query='no match')
        self.assertEqual((empty['source_sample_count'],empty['returned_count'],empty['tracks']), (0,0,[]))
        self.search_payload = {'tracks':{'items':[{'type':'episode','uri':'spotify:episode:wrong'}], 'total':1}}
        with self.assertRaises(HomeAssistantError):
            await self.service('search_tracks', query='mismatch')
        self.search_payload = {'tracks':{'items':[search_track(index) for index in range(30)], 'total':100}}
        bounded = await self.service('search_tracks', limit=20, query='large sample')
        self.assertEqual((bounded['source_sample_count'],bounded['returned_count'],len(bounded['tracks'])), (20,20,20))
        self.assertTrue(bounded['source_has_more'])
        self.assertLess(len(str(bounded).encode()), 65536)
        self.search_payload = {'tracks':{'items':[{'type':'track','uri':'spotify:track:'+'1'*22,
            'name':'x'*257,'artists':[{'name':'artist'}]}], 'total':1}}
        with self.assertRaises(HomeAssistantError):
            await self.service('search_tracks', query='oversized result')
        self.assertNotIn('NEVER_PUBLIC', str(bounded))
        self.assertEqual(self._writes(), [])

    async def test_search_owner_replacement_during_provider_wait_cannot_publish(self):
        entry = self.entries[0]
        original = entry.runtime_data
        self.search_held = True
        call = self._task(self.service('search_tracks', query='held search'))
        await asyncio.wait_for(self.search_entered.wait(), 2)
        entry.runtime_data = copy.copy(original)
        self.search_release.set()
        with self.assertRaises(HomeAssistantError):
            await call
        entry.runtime_data = original
        self.assertEqual(self._writes(), [])

    async def test_playback_uses_successful_poll_not_optimistic_entity_or_viewer_poll(self):
        data = self.entries[0].runtime_data
        response = await self.service('get_playback_snapshot')
        self.assertEqual(response['playback']['state'], 'paused')
        self.assertEqual(response['poll_sequence'], data.coordinator.data['poll_sequence'])
        self.assertEqual(response['owner_epoch'], data.coordinator.owner_epoch)
        entity_id = entity_registry.async_get(self.hass).async_get_entity_id(
            'media_player', 'podconnect', 'account-alpha_spotify-one')
        component = self.hass.data['entity_components']['media_player']
        entity = component.get_entity(entity_id)
        entity._optimistic_playing = True
        entity._optimistic_shuffle = True
        entity.async_write_ha_state()
        self.assertEqual(self.hass.states.get(entity_id).state, 'playing')
        observed = await self.service('get_playback_snapshot')
        self.assertEqual(observed['playback']['state'], 'paused')
        self.assertFalse(observed['playback']['shuffle'])
        self.assertFalse(observed['physical_result_verified'])
        self.assertEqual(self.calls, [])
        beta = await self.service('get_playback_snapshot', 'account-beta')
        self.assertNotEqual(beta['owner_epoch'], observed['owner_epoch'])
        with self.assertRaises(HomeAssistantError):
            await self.service('get_playback_snapshot', 'unknown')

    async def test_playback_stale_failure_no_active_and_newer_poll(self):
        coord = self.entries[0].runtime_data.coordinator
        original = copy.deepcopy(coord.data)
        coord.data['playback_observed_monotonic'] -= 16
        self.assertEqual((await self.service('get_playback_snapshot'))['error_kind'], 'stale')
        coord.data = copy.deepcopy(original)
        coord.last_update_success = False
        self.assertEqual((await self.service('get_playback_snapshot'))['error_kind'], 'unavailable')
        coord.last_update_success = True
        coord.data['playback'] = None
        empty = await self.service('get_playback_snapshot')
        self.assertEqual(empty['playback']['state'], 'no_active_playback')
        self.playback_override = {'is_playing': True, 'device': {'id': 'spotify-new', 'name': 'Other'},
                                 'item': track(), 'timestamp': 1234, 'progress_ms': 1000,
                                 'shuffle_state': False, 'repeat_state': 'off', 'authorization': 'NEVER_PUBLIC'}
        await coord.async_refresh()
        fresh = await self.service('get_playback_snapshot')
        self.assertGreater(fresh['poll_sequence'], original['poll_sequence'])
        self.assertEqual(fresh['playback']['target']['target_id'], 'spotify-new')
        self.assertEqual(fresh['playback']['provider_changed_at_ms'], 1234)
        self.assertEqual(fresh['playback']['item']['artwork']['url'],
            'https://i.scdn.co/image/ab67616d0000b2730123456789abcdef')
        self.assertNotIn('NEVER_PUBLIC', str(fresh))
        self.assertEqual(self._writes(), [])

    async def test_queue_http_shape_duplicates_cache_limit_and_account_isolation(self):
        response = await self.client.post('/api/services/podconnect/get_queue?return_response',
            json={'config_entry_id': 'account-alpha', 'limit': 1})
        self.assertEqual(response.status, 200)
        first = (await response.json())['service_response']
        self.assertTrue(first['truncated'])
        second = await self.service('get_queue', limit=20)
        self.assertEqual(self.queue_calls, 1)
        self.assertEqual(first['snapshot_id'], second['snapshot_id'])
        self.assertEqual(len(second['items']), 2)
        self.assertEqual(second['items'][0]['artwork']['kind'], 'album_cover')
        self.assertEqual(second['currently_playing']['artwork']['provider'], 'spotify')
        self.assertEqual(second['items'][0]['uri'], second['items'][1]['uri'])
        self.assertNotEqual(second['items'][0]['option_id'], second['items'][1]['option_id'])
        self.assertEqual(second['relation_to_playback'], 'not_atomic')
        self.assertNotIn('NEVER_PUBLIC', str(second))
        second['items'].clear()
        self.assertEqual(len((await self.service('get_queue'))['items']), 2)
        beta = await self.service('get_queue', 'account-beta')
        self.assertEqual(self.queue_calls, 2)
        self.assertNotEqual(beta['snapshot_id'], first['snapshot_id'])
        reader = self.entries[0].runtime_data.observations
        reader._queue_cached_at -= 11
        reader._queue_next_read_at = 0
        refreshed = await self.service('get_queue')
        self.assertNotEqual(refreshed['snapshot_id'], first['snapshot_id'])
        self.assertEqual(self.queue_calls, 3)
        self.assertEqual(self._writes(), [])

    async def test_queue_empty_episode_malformed_and_bounds(self):
        reader = self.entries[0].runtime_data.observations
        episode = {'type': 'episode', 'uri': 'spotify:episode:one', 'name': 'Episode', 'show': {'name': 'Show'}}
        for payload, expected in [
            ({'currently_playing': None, 'queue': []}, True),
            ({'currently_playing': episode, 'queue': [episode]}, True),
            ({'queue': [None]}, False),
            ({'queue': [{'type': 'future'}]}, False),
            ({'queue': [], 'ignored': 'x' * 270000}, False),
        ]:
            reader._queue_cache = None
            reader._queue_next_read_at = 0
            self.queue_payload = payload
            result = await self.service('get_queue')
            self.assertEqual(result['ok'], expected)
        for limit in (True, 0, 21, '1'):
            response = await self.client.post('/api/services/podconnect/get_queue?return_response',
                json={'config_entry_id': 'account-alpha', 'limit': limit})
            self.assertEqual(response.status, 400)
        response = await self.client.post('/api/services/podconnect/get_queue?return_response', json={})
        self.assertEqual(response.status, 400)
        self.assertEqual(self._writes(), [])

    async def test_queue_errors_redacted_and_negatively_cached(self):
        reader = self.entries[0].runtime_data.observations
        for status, kind in ((401, 'authentication_required'), (403, 'forbidden'), (429, 'rate_limited')):
            reader._queue_cache = None
            reader._queue_next_read_at = 0
            self.queue_status = status
            before = self.queue_calls
            result = await self.service('get_queue')
            self.assertEqual(result, {'ok': False, 'error_kind': kind})
            self.assertEqual(await self.service('get_queue'), result)
            self.assertEqual(self.queue_calls, before + 1)
            self.assertNotIn('NEVER_PUBLIC', str(result))
            if status == 429:
                reader._queue_cached_at -= 11
                self.assertEqual(await self.service('get_queue'), result)
                self.assertEqual(self.queue_calls, before + 1)

    async def test_queue_concurrency_and_cancellation_leave_no_orphan(self):
        self.queue_held = True
        reader = self.entries[0].runtime_data.observations
        call = self._task(self.service('get_queue'))
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        self.assertEqual(await self.service('get_queue'), {'ok': False, 'error_kind': 'busy'})
        self.assertEqual(self.queue_calls, 1)
        call.cancel()
        with self.assertRaises(asyncio.CancelledError):
            await call
        self.assertFalse(reader._queue_busy)
        self.assertIsNone(reader._queue_cache)
        self.queue_release.set()
        self.assertEqual((await self.service('get_queue'))['error_kind'], 'busy')
        self.assertEqual(self.queue_calls, 1)
        reader._queue_next_read_at = 0
        self.assertTrue((await self.service('get_queue'))['ok'])
        self.assertEqual(self._writes(), [])

    async def test_queue_unload_and_replacement_cannot_publish_held_result(self):
        self.queue_held = True
        entry = self.entries[0]
        original = entry.runtime_data
        call = self._task(self.service('get_queue'))
        await asyncio.wait_for(self.queue_entered.wait(), 2)
        # Exact runtime-data identity changes while the HTTP response is outstanding.
        replacement = copy.copy(original)
        entry.runtime_data = replacement
        self.queue_release.set()
        with self.assertRaises(HomeAssistantError):
            await call
        self.assertIsNone(original.observations._queue_cache)
        entry.runtime_data = original
        self.assertTrue(await self.hass.config_entries.async_unload(entry.entry_id))
        with self.assertRaises(HomeAssistantError):
            await self.service('get_queue')
        self.assertEqual(self._writes(), [])

    async def test_queue_actual_timeout_releases_reader(self):
        self.queue_held = True
        reader = self.entries[0].runtime_data.observations
        async with asyncio.timeout(9):
            result = await self.service('get_queue')
        self.assertEqual(result, {'ok': False, 'error_kind': 'unavailable'})
        self.assertFalse(reader._queue_busy)
        self.assertEqual(self.queue_calls, 1)
        self.assertEqual(await self.service('get_queue'), result)
        self.assertEqual(self.queue_calls, 1)

    async def test_playlist_search_exact_account_epoch_and_closed_real_context(self):
        self.search_payload = {'playlists': {'items': [{'type': 'playlist',
            'uri': 'spotify:playlist:' + 'P' * 22, 'name': 'Real cooking', 'secret': 'NEVER_PUBLIC'}]}}
        alpha, beta = self.entries
        with patch.object(alpha.runtime_data.api, 'search', side_effect=AssertionError('wrong account')):
            result = await self.hass.services.async_call('podconnect', 'search_playlists',
                {'config_entry_id': beta.entry_id, 'query': 'cooking', 'limit': 3},
                blocking=True, return_response=True)
        self.assertEqual(result['owner_epoch'], beta.runtime_data.coordinator.owner_epoch)
        self.assertEqual(result['config_entry_id'], beta.entry_id)
        self.assertEqual(result['playlists'], [{'uri': 'spotify:playlist:' + 'P' * 22, 'name': 'Real cooking'}])
        self.assertEqual(self.search_queries[-1], {'q': 'cooking', 'type': 'playlist', 'limit': '3'})
        self.assertNotIn('NEVER_PUBLIC', str(result))
        self.assertEqual(self._writes(), [])

    async def test_playlist_search_does_not_promote_track_or_missing_owner(self):
        self.search_payload = {'playlists': {'items': [search_track(0)]}}
        with self.assertRaises(HomeAssistantError):
            await self.service('search_playlists', query='cooking')
        with self.assertRaises(HomeAssistantError):
            await self.service('search_playlists', 'unknown-account', query='cooking')
        self.assertEqual(self._writes(), [])
