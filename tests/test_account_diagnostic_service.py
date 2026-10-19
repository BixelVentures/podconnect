"""Actual registered handler/client with inert HA and HTTP edges."""

import ast
import asyncio
import hashlib
import importlib.util
import json
import sys
import time
import unittest
from pathlib import Path
from types import ModuleType
from types import SimpleNamespace as NS
from unittest.mock import AsyncMock, patch

BASE = Path(__file__).parents[1] / "custom_components/podconnect"
ENGINE = "a" * 40


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


class OwnerError(Exception):
    pass


stubs = {
    key: ModuleType(key)
    for key in (
        "voluptuous",
        "homeassistant",
        "homeassistant.core",
        "homeassistant.exceptions",
        "aiohttp",
        "account_fixture",
        "account_fixture.const",
    )
}
stubs["account_fixture"].__path__ = [str(BASE)]
stubs["account_fixture.const"].DOMAIN = "podconnect"
stubs["homeassistant.core"].SupportsResponse = NS(ONLY="only")
stubs["homeassistant.exceptions"].HomeAssistantError = OwnerError
stubs["aiohttp"].ClientError = type("ClientError", (Exception,), {})
stubs["aiohttp"].ClientTimeout = lambda **kw: kw
for name in ("Required", "All", "Length", "Match", "Schema"):
    setattr(stubs["voluptuous"], name, lambda *args, **kw: args[0] if args else kw)
with patch.dict(sys.modules, stubs):
    load("account_fixture.account_diagnostic", BASE / "account_diagnostic.py")
    SERVICE = load(
        "account_fixture.account_diagnostic_service",
        BASE / "account_diagnostic_service.py",
    )
    CLIENT = load("account_fixture.speakers", BASE / "speakers.py")


def response():
    now = time.time_ns() // 1_000_000
    return {
        "schema": "podconnect.start_identity.v1",
        "engine_device_id": ENGINE,
        "account_observation": {
            "schema": "podconnect.native_account_observation.v1",
            "method": "sha256",
            "domain": "spotify.ap.canonical_username",
            "identifier_sha256": hashlib.sha256(b"fixture-account").hexdigest(),
            "engine_device_id": ENGINE,
            "run_generation": "1",
            "connection_generation": "2",
            "observation_sequence": "3",
            "observed_at_unix_ms": now - 1,
            "expires_at_unix_ms": now + 4999,
        },
    }


class Body:
    def __init__(self, raw):
        self.raw = raw

    async def iter_chunked(self, size):
        for pos in range(0, len(self.raw), size):
            yield self.raw[pos : pos + size]


class Reply:
    def __init__(self, raw):
        self.status, self.content = 200, Body(raw)

    async def __aenter__(self):
        return self

    async def __aexit__(self, *args):
        pass


class Web:
    def __init__(self):
        self.calls, self.raw, self.mutate = [], None, None

    def request(self, method, url, **kw):
        self.calls.append((method, url, kw))
        if self.mutate:
            self.mutate()
        return Reply(
            self.raw if self.raw is not None else json.dumps(response()).encode()
        )


class AccountServiceTest(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        self.web = Web()
        self.client = CLIENT.SpeakersClient(self.web, "http://native.invalid")
        self.profile = {"id": "fixture-account", "account_id": "never-used-account-id"}
        self.api = NS(current_user=AsyncMock(return_value=self.profile))
        self.data = NS(
            active=True,
            api=self.api,
            coordinator=NS(owner_epoch="epoch"),
            speakers=self.client,
        )
        self.entry = NS(
            entry_id="entry", unique_id="fixture-account", runtime_data=self.data
        )
        self.entries = [self.entry]
        self.registered = {}

        def register(domain, name, handler, **kw):
            self.registered[name] = (handler, kw)

        self.hass = NS(
            config_entries=NS(async_entries=lambda domain: self.entries),
            services=NS(
                has_service=lambda domain, name: name in self.registered,
                async_register=register,
            ),
        )
        SERVICE.register_account_diagnostic_service(self.hass)
        self.call = NS(data={"config_entry_id": "entry", "engine_device_id": ENGINE})
        self.handler = self.registered["get_account_identifier_diagnostic"][0]

    async def test_registered_once_actual_client_one_optin_get_no_auth(self):
        SERVICE.register_account_diagnostic_service(self.hass)
        self.assertEqual(len(self.registered), 1)
        result = await self.handler(self.call)
        self.assertEqual(
            result,
            {
                "comparison": "identifiers_equal",
                "identity_relation": "unknown",
                "stored_vs_fresh_web_id": "identifiers_equal",
                "native_vs_fresh_web_id": "identifiers_equal",
            },
        )
        self.assertEqual(len(self.web.calls), 1)
        method, url, options = self.web.calls[0]
        self.assertEqual(
            (method, url),
            ("GET", "http://native.invalid/api/start-volume/identity?account=1"),
        )
        self.assertFalse(options["allow_redirects"])
        self.assertEqual(options["headers"], {"Accept": "application/json"})
        self.assertEqual(
            self.registered["get_account_identifier_diagnostic"][1][
                "supports_response"
            ],
            "only",
        )

    async def test_same_entry_fresh_profile_discriminates_without_identity_mutation(
        self,
    ):
        for kind in (
            "stored_stale",
            "actual_different",
            "malformed",
            "account_id_only",
            "provider_error",
        ):
            with self.subTest(kind=kind):
                self.setUp()
                if kind == "stored_stale":
                    self.entry.unique_id = "old-web-id"
                elif kind == "actual_different":
                    self.entry.unique_id = "different-web-id"
                    self.profile["id"] = "different-web-id"
                elif kind == "malformed":
                    self.profile["id"] = None
                elif kind == "account_id_only":
                    self.profile.pop("id")
                    self.profile["account_id"] = "fixture-account"
                else:
                    self.api.current_user.side_effect = RuntimeError(
                        "private-provider-details"
                    )
                original_entry = dict(vars(self.entry))
                result = await self.handler(self.call)
                self.api.current_user.assert_awaited_once_with()
                self.assertEqual(vars(self.entry), original_entry)
                self.assertEqual(result["identity_relation"], "unknown")
                if kind == "stored_stale":
                    self.assertEqual(
                        result,
                        {
                            "comparison": "identifiers_different",
                            "identity_relation": "unknown",
                            "stored_vs_fresh_web_id": "identifiers_different",
                            "native_vs_fresh_web_id": "identifiers_equal",
                        },
                    )
                elif kind == "actual_different":
                    self.assertEqual(
                        result,
                        {
                            "comparison": "identifiers_different",
                            "identity_relation": "unknown",
                            "stored_vs_fresh_web_id": "identifiers_equal",
                            "native_vs_fresh_web_id": "identifiers_different",
                        },
                    )
                else:
                    self.assertEqual(
                        result,
                        {
                            "comparison": "unknown",
                            "identity_relation": "unknown",
                            "stored_vs_fresh_web_id": "unknown",
                            "native_vs_fresh_web_id": "unknown",
                        },
                    )
                encoded = json.dumps(result)
                for private in (
                    "fixture-account",
                    "different-web-id",
                    "old-web-id",
                    "never-used-account-id",
                    "private-provider-details",
                    response()["account_observation"]["identifier_sha256"],
                ):
                    self.assertNotIn(private, encoded)

    async def test_owner_replaced_during_fresh_profile_stops_before_native_read(self):
        async def replace_owner():
            self.data.coordinator.owner_epoch = "replacement"
            return self.profile

        self.api.current_user.side_effect = replace_owner
        with self.assertRaises(OwnerError):
            await self.handler(self.call)
        self.assertEqual(self.web.calls, [])

    async def test_owner_changes_during_await_reject(self):
        for kind in (
            "entry",
            "runtime",
            "api",
            "coordinator",
            "speakers",
            "unique_id",
            "epoch",
            "unload",
        ):
            with self.subTest(kind=kind):
                self.setUp()

                def mutate(kind=kind):
                    if kind == "entry":
                        self.entries[:] = [NS(**vars(self.entry))]
                    elif kind == "runtime":
                        self.entry.runtime_data = NS(**vars(self.data))
                    elif kind == "unique_id":
                        self.entry.unique_id = "other"
                    elif kind == "epoch":
                        self.data.coordinator.owner_epoch = "other"
                    elif kind == "unload":
                        self.data.active = False
                    else:
                        setattr(self.data, kind, object())

                self.web.mutate = mutate
                with self.assertRaises(OwnerError):
                    await self.handler(self.call)

    async def test_oversize_duplicate_crossengine_stale_unknown(self):
        foreign = response()
        foreign["account_observation"]["engine_device_id"] = "b" * 40
        stale = response()
        stale["account_observation"]["observed_at_unix_ms"] -= 10000
        stale["account_observation"]["expires_at_unix_ms"] -= 10000
        for raw in (
            b"x" * 65537,
            b'{"schema":"one","schema":"two"}',
            json.dumps(foreign).encode(),
            json.dumps(stale).encode(),
        ):
            self.web.raw = raw
            self.assertEqual(
                await self.handler(self.call),
                {
                    "comparison": "unknown",
                    "identity_relation": "unknown",
                    "stored_vs_fresh_web_id": "unknown",
                    "native_vs_fresh_web_id": "unknown",
                },
            )

    async def test_cancel_propagates_without_retry(self):
        async def cancelled():
            raise asyncio.CancelledError()

        self.client.account_identity = cancelled
        with self.assertRaises(asyncio.CancelledError):
            await self.handler(self.call)
        self.assertEqual(self.web.calls, [])

    def test_actual_setup_registers_service(self):
        tree = ast.parse((BASE / "__init__.py").read_text())
        setup = next(
            node
            for node in tree.body
            if isinstance(node, ast.AsyncFunctionDef)
            and node.name == "async_setup_entry"
        )
        calls = [
            node
            for node in ast.walk(setup)
            if isinstance(node, ast.Call)
            and isinstance(node.func, ast.Name)
            and node.func.id == "register_account_diagnostic_service"
        ]
        self.assertEqual(len(calls), 1)


if __name__ == "__main__":
    unittest.main()
