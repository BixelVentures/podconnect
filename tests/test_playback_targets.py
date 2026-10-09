"""Execute actual service/client bodies with inert HA/HTTP boundaries, no provider.

Like the existing control tests these load shipped AST bodies, not a HA runtime.
Registration/schema data, async client arguments and entry lifetime are observed;
actual HA core integration and physical playback require their separate gates.
"""

import ast
import asyncio
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import AsyncMock
from urllib.parse import urlsplit, urlunsplit

ROOT = Path(__file__).parents[1] / "custom_components/podconnect"


class HAError(Exception):
    pass


class SpotifyError(Exception):
    pass


class ClientError(Exception):
    pass


def load_body(file, namespace=None, names=None):
    tree = ast.parse((ROOT / file).read_text())
    body = [n for n in tree.body if not isinstance(n, (ast.Import, ast.ImportFrom))]
    if names is not None:
        body = [n for n in body if getattr(n, "name", None) in names]
    future = ast.ImportFrom(module="__future__", names=[ast.alias(name="annotations")], level=0)
    ns = dict(
        asyncio=asyncio,
        json=json,
        urlsplit=urlsplit,
        urlunsplit=urlunsplit,
        ClientError=ClientError,
        ClientTimeout=lambda **kw: kw,
    )
    ns.update(namespace or {})
    exec(
        compile(
            ast.fix_missing_locations(ast.Module(body=[future, *body], type_ignores=[])),
            str(ROOT / file),
            "exec",
        ),
        ns,
    )
    return ns


S = load_body("speakers.py")
V = SimpleNamespace(
    Required=lambda key: key,
    Optional=lambda key: key,
    In=lambda vals: vals,
    All=lambda *args: args,
    Length=lambda **kw: kw,
    Schema=lambda schema: schema,
)
T = load_body(
    "targets.py",
    dict(
        vol=V,
        SupportsResponse=SimpleNamespace(ONLY="only"),
        HomeAssistantError=HAError,
        SpotifyApiError=SpotifyError,
        SpeakersError=S["SpeakersError"],
        DOMAIN="podconnect",
    ),
)


def device(id="device-B", name="Room B", restricted=False):
    return dict(id=id, name=name, is_restricted=restricted)


def catalog():
    return {
        "binding": dict(ready=True, incarnation="inc-A", registry="registry-A", room_id="A"),
        "aliases": [dict(id=1, room_id="A", name="Same"), dict(id=2, room_id="B", name="Same")],
    }


class Response:
    def __init__(self, value=None, status=200, raw=None):
        self.status = status
        self.raw = raw if raw is not None else json.dumps(value).encode()
        self.content = self

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        return False

    async def iter_chunked(self, size):
        for i in range(0, len(self.raw), size):
            yield self.raw[i : i + size]


class Web:
    def __init__(self, replies):
        self.replies = list(replies)
        self.calls = []

    def request(self, *args, **kwargs):
        self.calls.append((args, kwargs))
        return self.replies.pop(0)


def services(api=None, speakers=None):
    api = api or SimpleNamespace(
        devices=AsyncMock(return_value=[device()]),
        playback_state=AsyncMock(return_value={"is_playing": False}),
        transfer=AsyncMock(),
    )
    data = SimpleNamespace(api=api, speakers=speakers, active=True)
    entries = [SimpleNamespace(entry_id="account-A", title="Account A", runtime_data=data)]
    registered = {}
    registry = SimpleNamespace(
        has_service=lambda domain, name: name in registered,
        async_register=lambda domain, name, handler, **kw: registered.__setitem__(
            name, (handler, kw)
        ),
    )
    hass = SimpleNamespace(
        services=registry, config_entries=SimpleNamespace(async_entries=lambda domain: entries)
    )
    T["register_target_services"](hass)
    return hass, data, entries, registered


async def invoke(reg, name, **kw):
    return await reg[name][0](SimpleNamespace(data={"config_entry_id": "account-A", **kw}))


class TargetTests(unittest.IsolatedAsyncioTestCase):
    async def test_explicit_entry_and_response_schemas_no_first_account_fallback(self):
        _, data, entries, reg = services()
        other = SimpleNamespace(api=SimpleNamespace(transfer=AsyncMock()), active=True)
        entries.append(SimpleNamespace(entry_id="account-B", runtime_data=other))
        self.assertEqual(set(reg), {"get_targets", "move_playback"})
        self.assertEqual(reg["get_targets"][1]["supports_response"], "only")
        self.assertIn("config_entry_id", reg["move_playback"][1]["schema"])
        with self.assertRaises(HAError):
            await invoke(
                reg,
                "move_playback",
                config_entry_id="absent",
                kind="spotify_device",
                target_id="device-B",
            )
        data.api.devices.assert_not_awaited()
        other.api.transfer.assert_not_awaited()

    async def test_stable_spotify_id_and_known_paused_state(self):
        _, data, _, reg = services()
        reply = await invoke(reg, "move_playback", kind="spotify_device", target_id="device-B")
        data.api.transfer.assert_awaited_once_with("device-B", play=False)
        self.assertTrue(reply["provider_request_accepted"])
        self.assertNotIn("playing", reply)

    async def test_restricted_disappeared_duplicate_and_unknown_playback_refuse(self):
        for rows, pb in (
            ([device(restricted=True)], {"is_playing": True}),
            ([], {"is_playing": True}),
            ([device(), device()], {"is_playing": False}),
            ([device()], None),
            ([device()], {"is_playing": 1}),
        ):
            with self.subTest(rows=rows, pb=pb):
                _, data, _, reg = services()
                data.api.devices.return_value = rows
                data.api.playback_state.return_value = pb
                with self.assertRaises(HAError):
                    await invoke(reg, "move_playback", kind="spotify_device", target_id="device-B")
                data.api.transfer.assert_not_awaited()

    async def test_fresh_name_lookup_refuses_duplicates_and_accepts_unique_changed_id(self):
        api = SimpleNamespace(
            devices=AsyncMock(return_value=[device("new-ID", "Same")]),
            playback_state=AsyncMock(return_value={"is_playing": True}),
        )
        self.assertEqual(await T["spotify_transfer_target"](api, name="Same"), ("new-ID", True))
        api.devices.return_value = [device("one", "Same"), device("two", "Same")]
        with self.assertRaises(HAError):
            await T["spotify_transfer_target"](api, name="Same")

    async def test_entity_select_source_uses_actual_fresh_helper_and_send(self):
        tree = ast.parse((ROOT / "media_player.py").read_text())
        method = next(
            n
            for n in ast.walk(tree)
            if isinstance(n, ast.AsyncFunctionDef) and n.name == "async_select_source"
        )
        ns = dict(
            spotify_transfer_target=T["spotify_transfer_target"],
            SpotifyApiError=SpotifyError,
            ClientError=ClientError,
            HomeAssistantError=HAError,
        )
        exec(
            compile(ast.Module(body=[method], type_ignores=[]), "actual select_source", "exec"), ns
        )
        _, data, _, _ = services()

        async def send(coro):
            await coro

        entry = SimpleNamespace(entry_id="account-A", runtime_data=data)
        hass = SimpleNamespace(config_entries=SimpleNamespace(async_get_entry=lambda key: entry))
        coordinator = SimpleNamespace(api=data.api, config_entry=entry, hass=hass)
        data.coordinator = coordinator
        obj = SimpleNamespace(coordinator=coordinator, _send=send)
        await ns["async_select_source"](obj, "Room B")
        data.api.transfer.assert_awaited_once_with("device-B", play=False)
        data.api.devices.return_value = [device("1"), device("2")]
        with self.assertRaises(HAError):
            await ns["async_select_source"](obj, "Room B")
        self.assertEqual(data.api.transfer.await_count, 1)

    async def test_unload_or_replaced_entry_during_lookup_cannot_admit(self):
        for replace in (False, True):
            _, data, entries, reg = services()

            async def playback():
                if replace:
                    entries[0].runtime_data = SimpleNamespace(active=True)
                else:
                    data.active = False
                return {"is_playing": True}

            data.api.playback_state.side_effect = playback
            with self.assertRaises(HAError):
                await invoke(reg, "move_playback", kind="spotify_device", target_id="device-B")
            data.api.transfer.assert_not_awaited()

    async def test_account_discovery_never_queries_or_chooses_a_backend(self):
        _, data, entries, reg = services()
        entries.extend(
            [
                SimpleNamespace(
                    entry_id="account-B", title="Same", runtime_data=SimpleNamespace(active=True)
                ),
                SimpleNamespace(
                    entry_id="unloaded", title="Gone", runtime_data=SimpleNamespace(active=False)
                ),
            ]
        )
        entries[0].title = "Same"
        result = await reg["get_targets"][0](SimpleNamespace(data={}))
        self.assertEqual(
            result,
            {
                "accounts": [
                    {"config_entry_id": "account-A", "title": "Same"},
                    {"config_entry_id": "account-B", "title": "Same"},
                ]
            },
        )
        data.api.devices.assert_not_awaited()
        data.api.playback_state.assert_not_awaited()
        data.api.transfer.assert_not_awaited()
        entries.append(entries[0])
        with self.assertRaises(HAError):
            await reg["get_targets"][0](SimpleNamespace(data={}))

    async def test_entity_held_lookup_retirement_and_replacement_veto_actual_transfer(self):
        tree = ast.parse((ROOT / "media_player.py").read_text())
        method = next(
            n
            for n in ast.walk(tree)
            if isinstance(n, ast.AsyncFunctionDef) and n.name == "async_select_source"
        )
        ns = dict(
            spotify_transfer_target=T["spotify_transfer_target"],
            SpotifyApiError=SpotifyError,
            ClientError=ClientError,
            HomeAssistantError=HAError,
        )
        exec(
            compile(ast.Module(body=[method], type_ignores=[]), "actual select_source", "exec"), ns
        )
        for phase in ("devices", "playback_state"):
            for retirement in ("unload", "runtime", "entry"):
                with self.subTest(phase=phase, retirement=retirement):
                    _, data, _, _ = services()
                    entry = SimpleNamespace(entry_id="account-A", runtime_data=data)
                    current_entry = [entry]
                    hass = SimpleNamespace(
                        config_entries=SimpleNamespace(async_get_entry=lambda key: current_entry[0])
                    )
                    coordinator = SimpleNamespace(api=data.api, config_entry=entry, hass=hass)
                    data.coordinator = coordinator
                    entered, release = asyncio.Event(), asyncio.Event()

                    async def held():
                        entered.set()
                        await release.wait()
                        return [device()] if phase == "devices" else {"is_playing": False}

                    getattr(data.api, phase).side_effect = held

                    async def send(coro):
                        await coro

                    obj = SimpleNamespace(coordinator=coordinator, _send=send)
                    task = asyncio.create_task(ns["async_select_source"](obj, "Room B"))
                    try:
                        await asyncio.wait_for(entered.wait(), 1)
                        if retirement == "unload":
                            data.active = False
                        elif retirement == "runtime":
                            entry.runtime_data = SimpleNamespace(
                                active=True, coordinator=coordinator
                            )
                        else:
                            current_entry[0] = SimpleNamespace(
                                entry_id="account-A", runtime_data=data
                            )
                        release.set()
                        with self.assertRaises(HAError):
                            await asyncio.wait_for(task, 1)
                        data.api.transfer.assert_not_awaited()
                    finally:
                        release.set()
                        if not task.done():
                            task.cancel()
                        await asyncio.gather(task, return_exceptions=True)

    async def test_local_stable_room_same_name_no_oauth_and_exact_claim(self):
        c = catalog()
        reply = {"accepted_local": True, "binding": {**c["binding"], "room_id": "B"}}
        web = Web([Response(c), Response(reply)])
        _, data, _, reg = services(speakers=S["SpeakersClient"](web, "http://local:8099"))
        result = await invoke(reg, "move_playback", kind="configured_alias", target_id="B")
        self.assertTrue(result["accepted_local"])
        data.api.transfer.assert_not_awaited()
        self.assertEqual(web.calls[1][0], ("POST", "http://local:8099/api/room-switch"))
        self.assertEqual(
            web.calls[1][1]["json"],
            {"room_id": "B", "expected_incarnation": "inc-A", "expected_registry": "registry-A"},
        )
        for _, kw in web.calls:
            self.assertEqual(kw["headers"], {"Accept": "application/json"})
            self.assertFalse(kw["allow_redirects"])
            self.assertEqual(kw["timeout"], {"total": 4})

    async def test_local_malformed_duplicate_missing_target_zero_post(self):
        for change in ("duplicate", "missing", "retired", "malformed"):
            c = catalog()
            if change == "duplicate":
                c["aliases"].append(c["aliases"][1])
            if change == "missing":
                c["aliases"].pop()
            if change == "retired":
                c["binding"]["ready"] = False
            if change == "malformed":
                c["aliases"][1]["id"] = True
            web = Web([Response(c)])
            _, _, _, reg = services(speakers=S["SpeakersClient"](web, "http://local"))
            with self.assertRaises(HAError):
                await invoke(reg, "move_playback", kind="configured_alias", target_id="B")
            self.assertEqual(len(web.calls), 1)

    async def test_local_stale_rejection_or_unknown_commit_never_retries(self):
        for response in (
            Response(status=409),
            Response({"accepted_local": False, "binding": catalog()["binding"]}),
            Response(raw=b"not-json"),
        ):
            web = Web([Response(catalog()), response])
            _, _, _, reg = services(speakers=S["SpeakersClient"](web, "http://local"))
            with self.assertRaises(HAError):
                await invoke(reg, "move_playback", kind="configured_alias", target_id="B")
            self.assertEqual(len(web.calls), 2)

    async def test_read_only_observed_kind_zero_dispatch(self):
        web = Web([])
        _, data, _, reg = services(speakers=S["SpeakersClient"](web, "http://local"))
        with self.assertRaises(HAError):
            await invoke(reg, "move_playback", kind="observed_output", target_id="42")
        self.assertEqual(web.calls, [])
        data.api.devices.assert_not_awaited()

    async def test_cloud_failure_keeps_local_observation_and_configured_not_physical(self):
        web = Web(
            [
                Response(catalog()),
                Response(
                    {"rooms": [{"id": "A", "homepod_id": "42"}, {"id": "B", "homepod_id": "43"}]}
                ),
                Response(
                    {
                        "owntone_up": True,
                        "devices": [
                            {"id": "43", "name": "Same", "selected": False, "needs_auth": True}
                        ],
                    }
                ),
            ]
        )
        _, data, _, reg = services(speakers=S["SpeakersClient"](web, "http://local"))
        data.api.devices.side_effect = ClientError()
        result = await invoke(reg, "get_targets")
        self.assertEqual(result["errors"], {"spotify_device": "unavailable"})
        self.assertEqual(
            [r["kind"] for r in result["targets"]],
            ["configured_alias", "configured_alias", "observed_output"],
        )
        self.assertEqual(result["targets"][1]["homepod_id"], "43")
        self.assertNotIn("playing", result["targets"][1])
        self.assertTrue(result["targets"][2]["read_only"])

    async def test_local_failure_keeps_cloud_and_missing_option_spotify_only(self):
        for local in (
            None,
            S["SpeakersClient"](Web([Response(status=503), Response(status=503)]), "http://local"),
        ):
            _, _, _, reg = services(speakers=local)
            result = await invoke(reg, "get_targets")
            self.assertEqual(result["targets"][0]["kind"], "spotify_device")
            self.assertIn("configured_alias", result["errors"])

    async def test_local_redirect_oversize_duplicate_json_and_cancellation(self):
        for response in (
            Response(status=302),
            Response(raw=b"x" * 65537),
            Response(raw=b'{"binding":{},"binding":{}}'),
        ):
            client = S["SpeakersClient"](Web([response]), "http://local")
            with self.assertRaises(S["SpeakersError"]):
                await client.aliases()

        class CancelWeb:
            def request(self, *a, **kw):
                raise asyncio.CancelledError

        with self.assertRaises(asyncio.CancelledError):
            await S["SpeakersClient"](CancelWeb(), "http://local").aliases()

    def test_url_has_no_credential_token_or_default(self):
        self.assertEqual(S["speakers_url"]("http://local:8099/base/"), "http://local:8099/base")
        for value in (
            "",
            "ftp://host",
            "http://user:pass@host",
            "http://host/?token=x",
            "http://host/#x",
            "http://host:bad",
        ):
            with self.subTest(value=value), self.assertRaises(ValueError):
                S["speakers_url"](value)

    async def test_options_preserve_oauth_and_other_options_and_allow_clear(self):
        class Flow:
            def async_create_entry(self, **kw):
                return kw

            def async_show_form(self, **kw):
                return kw

        flow = load_body(
            "config_flow.py",
            dict(
                OptionsFlow=Flow,
                CONF_SPEAKERS_URL="speakers_url",
                vol=SimpleNamespace(Optional=lambda key, **kw: key, Schema=lambda s: s),
                speakers_url=S["speakers_url"],
            ),
            names={"PodConnectOptionsFlow"},
        )["PodConnectOptionsFlow"]
        entry = SimpleNamespace(options={"unrelated": 1}, data={"token": "secret"})
        obj = flow(entry)
        result = await obj.async_step_init({"speakers_url": "http://local/"})
        self.assertEqual(result["data"], {"unrelated": 1, "speakers_url": "http://local"})
        self.assertEqual(entry.data, {"token": "secret"})
        self.assertEqual(entry.options, {"unrelated": 1})
        self.assertEqual(
            (await obj.async_step_init({"speakers_url": ""}))["data"], {"unrelated": 1}
        )
        self.assertEqual(
            (await obj.async_step_init({"speakers_url": "http://user@host"}))["errors"],
            {"speakers_url": "invalid_url"},
        )

    async def test_actual_unload_and_options_reload_owners(self):
        ns = load_body(
            "__init__.py",
            dict(PLATFORMS=["media_player"]),
            names={"async_unload_entry", "_options_updated"},
        )
        hass, data, entries, reg = services()
        hass.config_entries.async_unload_platforms = AsyncMock(return_value=False)
        hass.config_entries.async_reload = AsyncMock()
        self.assertFalse(await ns["async_unload_entry"](hass, entries[0]))
        self.assertTrue(data.active)
        hass.config_entries.async_unload_platforms.return_value = True
        self.assertTrue(await ns["async_unload_entry"](hass, entries[0]))
        self.assertFalse(data.active)
        with self.assertRaises(HAError):
            await invoke(reg, "get_targets")
        await ns["_options_updated"](hass, entries[0])
        hass.config_entries.async_reload.assert_awaited_once_with("account-A")

    async def test_actual_setup_admits_only_after_platforms_and_owns_option_listener(self):
        for failure in (False, True):
            _, data, _, _ = services()
            registered = []
            entry = SimpleNamespace(
                entry_id="account-A",
                options={"speakers_url": "http://local"},
                async_on_unload=lambda fn: registered.append(fn),
                add_update_listener=lambda fn: ("listener", fn),
            )

            async def forward(e, platforms):
                self.assertFalse(e.runtime_data.active)
                if failure:
                    raise RuntimeError("platform failed")

            hass = SimpleNamespace(
                config_entries=SimpleNamespace(async_forward_entry_setups=forward)
            )
            token = SimpleNamespace(
                token={"scope": "scope-A"}, async_ensure_token_valid=AsyncMock()
            )
            coord = SimpleNamespace(async_config_entry_first_refresh=AsyncMock())
            ns = load_body(
                "__init__.py",
                dict(
                    async_get_config_entry_implementation=AsyncMock(return_value=object()),
                    ImplementationUnavailableError=type(
                        "ImplementationUnavailableError", (Exception,), {}
                    ),
                    OAuth2Session=lambda *a: token,
                    aiohttp=SimpleNamespace(ClientError=ClientError),
                    ConfigEntryNotReady=RuntimeError,
                    ConfigEntryAuthFailed=RuntimeError,
                    SPOTIFY_SCOPES=["scope-A"],
                    SpotifyApi=lambda *a: data.api,
                    PodConnectCoordinator=lambda *a: coord,
                    CONF_SPEAKERS_URL="speakers_url",
                    SpeakersClient=S["SpeakersClient"],
                    async_get_clientsession=lambda h: Web([]),
                    PodConnectData=lambda **kw: SimpleNamespace(**kw, active=False),
                    _register_services=lambda h: None,
                    register_target_services=lambda h: None,
                    _options_updated=object(),
                    PLATFORMS=["media_player"],
                ),
                names={"async_setup_entry"},
            )
            if failure:
                with self.assertRaisesRegex(RuntimeError, "platform failed"):
                    await ns["async_setup_entry"](hass, entry)
                self.assertFalse(entry.runtime_data.active)
            else:
                self.assertTrue(await ns["async_setup_entry"](hass, entry))
                self.assertTrue(entry.runtime_data.active)
            self.assertEqual(len(registered), 1)
            self.assertEqual(registered[0][0], "listener")

    def test_reauth_remains_exact_original_options_preserving_hook(self):
        tree = ast.parse((ROOT / "config_flow.py").read_text())
        oauth = next(
            n
            for n in ast.walk(tree)
            if isinstance(n, ast.AsyncFunctionDef) and n.name == "async_oauth_create_entry"
        )
        calls = [
            n
            for n in ast.walk(oauth)
            if isinstance(n, ast.Call)
            and isinstance(n.func, ast.Attribute)
            and n.func.attr == "async_update_reload_and_abort"
        ]
        self.assertEqual(len(calls), 1)
        self.assertEqual({k.arg for k in calls[0].keywords}, {"title", "data"})

    def test_idempotent_registration_and_no_library_override(self):
        hass, _, _, reg = services()
        old = dict(reg)
        T["register_target_services"](hass)
        self.assertEqual(reg, old)


if __name__ == "__main__":
    unittest.main()
