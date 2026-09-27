"""Shipped methods/service registration with mocked HA/provider boundaries."""
import ast
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock
from test_control_errors import SpotifyApiError, HomeAssistantError

ROOT = Path(__file__).parents[1] / 'custom_components/podconnect'
MEDIA = SimpleNamespace(**{k:k.lower() for k in ('TRACK','ARTIST','ALBUM','PLAYLIST','PODCAST','EPISODE')})


def methods(file, names, namespace=None):
    tree = ast.parse((ROOT/file).read_text())
    nodes = [n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
    if not nodes:
        nodes = [n for n in ast.walk(tree) if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name in names]
    future = ast.ImportFrom(module='__future__', names=[ast.alias(name='annotations')], level=0)
    ns = dict(SpotifyApiError=SpotifyApiError, HomeAssistantError=HomeAssistantError, MediaClass=MEDIA,
              SearchMedia=SimpleNamespace, _SEARCH_KINDS={'tracks':('track','track')})
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
                entries = [] if case == 'unavailable' else [SimpleNamespace(runtime_data=SimpleNamespace(api=api))]
                hass = SimpleNamespace(services=SimpleNamespace(has_service=lambda *args:False,
                    async_register=lambda domain,name,handler,**kwargs:registered.__setitem__(name,handler)),
                    config_entries=SimpleNamespace(async_entries=lambda domain:entries))
                ns = methods('__init__.py',['_register_services','_track_list'],
                    {'DOMAIN':'podconnect','_LIBRARY_SERVICES':('top_tracks','recently_played','liked'),'SupportsResponse':SimpleNamespace(ONLY=1)})
                ns['_register_services'](hass)
                if case == 'empty': self.assertEqual(await registered['top_tracks'](None), {'tracks':[]})
                else:
                    with self.assertRaises(HomeAssistantError): await registered['top_tracks'](None)
