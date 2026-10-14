"""Shipped methods/service registration with mocked HA/provider boundaries."""
import ast
from pathlib import Path
import json
import re
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock
from test_control_errors import SpotifyApiError, HomeAssistantError

ROOT = Path(__file__).parents[1] / 'custom_components/podconnect'
MEDIA = SimpleNamespace(**{k:k.lower() for k in ('TRACK','ARTIST','ALBUM','PLAYLIST','PODCAST','EPISODE')})
VOL = SimpleNamespace(Optional=lambda key, **kwargs:key, Required=lambda key, **kwargs:key,
                      All=lambda *validators:validators, Length=lambda **kwargs:kwargs,
                      Schema=lambda schema:schema, Invalid=ValueError)


def methods(file, names, namespace=None):
    tree = ast.parse((ROOT/file).read_text())
    nodes = [n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
    if not nodes:
        nodes = [n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
    future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
    ns = dict(SpotifyApiError=SpotifyApiError, HomeAssistantError=HomeAssistantError, MediaClass=MEDIA,
              SearchMedia=SimpleNamespace, _SEARCH_KINDS={'tracks':('track','track')},
              vol=VOL, json=json, re=re,
              _SEARCH_URI=re.compile(r'^spotify:track:[A-Za-z0-9]{22}$'),
              _SEARCH_MAX_LIMIT=20, _SEARCH_MAX_REPLY_BYTES=65536)
    ns.update(namespace or {})
    exec(compile(ast.fix_missing_locations(ast.Module(body=[future,*nodes],type_ignores=[])), str(ROOT/file), 'exec'), ns)
    return ns


class SearchLibraryTruth(unittest.IsolatedAsyncioTestCase):
    async def test_requested_artist_source_order_survives_popularity(self):
        rows = [None, {'name':'Halo','artists':[{'name':'Beyonce'}],'popularity':60,'uri':'spotify:track:requested'},
                {'name':'Halo','artists':[{'name':'Cover Artist'}],'popularity':99,'uri':'spotify:track:cover'}]
        obj = SimpleNamespace(coordinator=SimpleNamespace(api=SimpleNamespace(search=AsyncMock(return_value={'tracks':{'items':rows}}))),
                              _result_item=lambda item,*args:SimpleNamespace(media_content_id=item['uri']))
        ns = methods('media_player.py', ['_search_top_uri','async_search_media'])
        self.assertEqual(await ns['_search_top_uri'](obj,'Halo Beyonce','music'), rows[1]['uri'])
        result = await ns['async_search_media'](obj,SimpleNamespace(search_query='Halo Beyonce',media_filter_classes=['track']))
        self.assertEqual([r.media_content_id for r in result.result], [rows[1]['uri'],rows[2]['uri']])

    async def test_assist_search_error_is_not_empty_success(self):
        obj = SimpleNamespace(coordinator=SimpleNamespace(api=SimpleNamespace(search=AsyncMock(side_effect=SpotifyApiError('HTTP 429')))))
        ns = methods('media_player.py',['async_search_media'])
        with self.assertRaisesRegex(HomeAssistantError,'429'):
            await ns['async_search_media'](obj,SimpleNamespace(search_query='song',media_filter_classes=[]))

    async def test_library_services_distinguish_empty_error_and_unavailable(self):
        for case in ('empty','failure','unavailable'):
            with self.subTest(case=case):
                registered = {}
                api = SimpleNamespace(top_tracks=AsyncMock(return_value=[]))
                if case == 'failure': api.top_tracks.side_effect = SpotifyApiError('HTTP 401')
                entries = [] if case == 'unavailable' else [SimpleNamespace(entry_id='one', domain='podconnect',
                    runtime_data=SimpleNamespace(api=api, active=True))]
                hass = SimpleNamespace(services=SimpleNamespace(has_service=lambda *args:False,
                    async_register=lambda domain,name,handler,**kwargs:registered.__setitem__(name,handler)),
                    config_entries=SimpleNamespace(async_entries=lambda domain:entries))
                ns = methods('__init__.py',['_register_services','_track_list','_library_owner',
                    '_search_query','_search_limit','_search_projection'],
                    {'DOMAIN':'podconnect','_LIBRARY_SERVICES':('top_tracks','recently_played','liked'),
                     'SupportsResponse':SimpleNamespace(ONLY=1), 'vol':VOL,
                     'spotify_album_artwork':lambda track: None,
                     'current':lambda hass, entry_id, expected=None: expected or entries[0].runtime_data,
                     })
                ns['_register_services'](hass)
                call = SimpleNamespace(data={})
                if case == 'empty': self.assertEqual(await registered['top_tracks'](call), {'tracks':[], 'config_entry_id':'one'})
                else:
                    with self.assertRaises(HomeAssistantError): await registered['top_tracks'](call)

    async def test_library_services_bind_exact_account_and_keep_single_account_compatibility(self):
        calls = []
        entries = []
        for account in ('alpha', 'beta'):
            async def top_tracks(account=account):
                calls.append(account)
                return [{'name': account, 'uri': f'spotify:track:{account}', 'artists': []}]
            entries.append(SimpleNamespace(entry_id=account, domain='podconnect',
                runtime_data=SimpleNamespace(api=SimpleNamespace(top_tracks=top_tracks), active=True)))
        by_id = {entry.entry_id:entry for entry in entries}
        registered = {}
        hass = SimpleNamespace(services=SimpleNamespace(has_service=lambda *args:False,
            async_register=lambda domain,name,handler,**kwargs:registered.__setitem__(name,handler)),
            config_entries=SimpleNamespace(async_entries=lambda domain:entries,
                async_get_entry=lambda entry_id:by_id.get(entry_id)))
        ns = methods('__init__.py',['_register_services','_track_list','_library_owner',
            '_search_query','_search_limit','_search_projection'],
            {'DOMAIN':'podconnect','_LIBRARY_SERVICES':('top_tracks','recently_played','liked'),
             'SupportsResponse':SimpleNamespace(ONLY=1), 'vol':VOL,
             'spotify_album_artwork':lambda track:None,
             'current':lambda hass, entry_id, expected=None: (
                 by_id[entry_id].runtime_data if entry_id in by_id and
                 (expected is None or by_id[entry_id].runtime_data is expected)
                 else (_ for _ in ()).throw(HomeAssistantError('PodConnect account entry unavailable'))),
             })
        ns['_register_services'](hass)
        self.assertEqual((await registered['top_tracks'](SimpleNamespace(data={'config_entry_id':'beta'})))['tracks'][0]['name'], 'beta')
        self.assertEqual(calls, ['beta'])
        with self.assertRaises(HomeAssistantError):
            await registered['top_tracks'](SimpleNamespace(data={}))
        entries.pop()
        self.assertEqual((await registered['top_tracks'](SimpleNamespace(data={})))['tracks'][0]['name'], 'alpha')

    def test_track_artwork_contract_is_closed_and_optional(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location('podconnect_artwork_under_test', ROOT/'artwork.py')
        artwork_module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(artwork_module)
        project = artwork_module.spotify_album_artwork
        image = 'https://i.scdn.co/image/ab67616d0000b2730123456789abcdef'
        self.assertEqual(project({'album':{'images':[{'url':image,'height':640,'width':640,'token':'SECRET'}]}}),
            {'kind':'album_cover','provider':'spotify','url':image})
        for url in ('http://i.scdn.co/image/abc', 'https://attacker.example/image/abc',
                    'https://user@i.scdn.co/image/abc', 'https://i.scdn.co/image/abc?token=secret',
                    'https://i.scdn.co/image/../abc'):
            self.assertIsNone(project({'album':{'images':[{'url':url}]}}), url)
        self.assertEqual(project({'album':{'images':[{'url':'https://attacker.example/image/x'}, {'url':image}]}}),
            {'kind':'album_cover','provider':'spotify','url':image})

    def test_search_projection_is_closed_bounded_account_and_source_sample(self):
        artwork = {'kind':'album_cover','provider':'spotify',
                   'url':'https://i.scdn.co/image/ab67616d0000b2730123456789abcdef'}
        module = methods('__init__.py', ['_search_query','_search_limit','_search_projection'], {
            'vol':VOL,'ServiceResponse':dict,'HomeAssistantError':HomeAssistantError,
            'spotify_album_artwork':lambda track:artwork if track.get('album') else None,
            'json':json,'re':re,'_SEARCH_URI':re.compile(r'^spotify:track:[A-Za-z0-9]{22}$'),
            '_SEARCH_MAX_LIMIT':20,'_SEARCH_MAX_REPLY_BYTES':65536,
        })
        self.assertEqual(module['_search_query']('  jazz guitar  '), 'jazz guitar')
        self.assertEqual(module['_search_limit'](3), 3)
        for query in ('  ', 'x' * 161, 42):
            with self.assertRaises(ValueError): module['_search_query'](query)
        for limit in (True, 0, 21, '3'):
            with self.assertRaises(ValueError): module['_search_limit'](limit)
        rows = [{'type':'track','name':'Song','uri':f'spotify:track:{str(i).zfill(22)}',
                 'artists':[{'name':'Artist'}], 'album':{'images':[{'url':artwork['url']}]},
                 'access_token':'SECRET'} for i in range(4)]
        response = module['_search_projection']({'tracks':{'items':rows,'total':12}}, 'entry-a', 'jazz', 3)
        self.assertEqual((response['config_entry_id'],response['source'],response['coverage']),
                         ('entry-a','spotify_web_api','spotify_search_sample'))
        self.assertEqual((response['source_sample_count'],response['returned_count'],response['source_has_more']),
                         (3,3,True))
        self.assertEqual(len(response['tracks']),3)
        self.assertEqual(response['tracks'][0]['artwork'],artwork)
        self.assertNotIn('SECRET',json.dumps(response))
        exact = module['_search_projection']({'tracks':{'items':rows[:3],'total':3}}, 'entry-a', 'jazz', 3)
        self.assertFalse(exact['source_has_more'])
        inconsistent = module['_search_projection']({'tracks':{'items':rows[:3],'total':1}}, 'entry-a', 'jazz', 3)
        self.assertIsNone(inconsistent['source_has_more'])
        with self.assertRaises(HomeAssistantError):
            module['_search_projection']({'tracks':{'items':[{'type':'episode'}]}}, 'entry-a', 'jazz', 3)
