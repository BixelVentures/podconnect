"""Token-data renewal keeps the real HA runtime owner; inert existing HA fixture."""

import asyncio
import time
import unittest
from types import MappingProxyType
from unittest.mock import AsyncMock, patch

import test_targets as fixture


class OAuthOwnerUpdateTests(unittest.IsolatedAsyncioTestCase):
    asyncSetUp = fixture.TargetFrameworkTests.asyncSetUp
    _cleanup = fixture.TargetFrameworkTests._cleanup
    _task = fixture.TargetFrameworkTests._task
    _backend = fixture.TargetFrameworkTests._backend
    _writes = fixture.TargetFrameworkTests._writes
    _service = fixture.TargetFrameworkTests._service

    async def test_token_refresh_keeps_runtime_and_cached_read_available(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        old_token = dict(entry.data["token"])
        object.__setattr__(
            entry,
            "data",
            MappingProxyType(
                {**entry.data, "token": {**old_token, "expires_at": time.time() - 1}}
            ),
        )
        fresh_token = {
            **old_token,
            "expires_at": time.time() + 86400,
            "access_token": "inert-fixture-only",
        }
        entered, release = asyncio.Event(), asyncio.Event()
        self.holds.append(release)
        unload = self.hass.config_entries.async_unload_platforms

        async def held_unload(selected, platforms):
            if selected is entry:
                entered.set()
                await release.wait()
            return await unload(selected, platforms)

        try:
            with (
                patch.object(
                    self.hass.config_entries, "async_unload_platforms", held_unload
                ),
                patch.object(
                    owner.session.implementation,
                    "async_refresh_token",
                    AsyncMock(return_value=fresh_token),
                ),
            ):
                await owner.session.async_ensure_token_valid()
                try:
                    await asyncio.wait_for(entered.wait(), 0.3)
                except TimeoutError:
                    pass
                response = await self.client.post(
                    "/api/services/podconnect/get_playback_snapshot?return_response",
                    json={"config_entry_id": entry.entry_id},
                )
                self.assertEqual(
                    response.status,
                    200,
                    "token-only refresh retired the loaded owner during cached read",
                )
                self.assertTrue(owner.active)
                self.assertIs(entry.runtime_data, owner)
                self.assertFalse(
                    entered.is_set(), "token-only refresh must not reload the account"
                )
                self.assertFalse(self._writes())
        finally:
            release.set()
            await self.hass.async_block_till_done()

    async def test_non_token_data_update_replaces_the_owner(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        self.hass.config_entries.async_update_entry(
            entry, data={**entry.data, "account_identity": "replacement"}
        )
        await self.hass.async_block_till_done()
        self.assertIsNot(entry.runtime_data, owner)
        self.assertFalse(owner.active)
        self.assertTrue(entry.runtime_data.active)
        self.assertFalse(self._writes())

    async def test_unique_identity_update_replaces_the_owner(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        self.hass.config_entries.async_update_entry(
            entry, unique_id="replacement-account"
        )
        await self.hass.async_block_till_done()
        self.assertIsNot(entry.runtime_data, owner)
        self.assertFalse(owner.active)
        self.assertTrue(entry.runtime_data.active)
        self.assertFalse(self._writes())

    async def test_explicit_reauthentication_still_reloads_token_only_data(self):
        from custom_components.podconnect.config_flow import PodConnectFlowHandler

        entry = self.entries[0]
        owner = entry.runtime_data
        flow = PodConnectFlowHandler()
        flow.hass = self.hass
        flow.context = {"source": "reauth", "entry_id": entry.entry_id}
        result = flow.async_update_reload_and_abort(
            entry,
            data={
                **entry.data,
                "token": {**entry.data["token"], "expires_at": time.time() + 90000},
            },
        )
        await self.hass.async_block_till_done()
        self.assertEqual(result["type"], "abort")
        self.assertIsNot(entry.runtime_data, owner)
        self.assertFalse(owner.active)
        self.assertTrue(entry.runtime_data.active)
        self.assertFalse(self._writes())

    async def test_concurrent_token_refresh_retains_one_runtime_owner(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        old_token = dict(entry.data["token"])
        object.__setattr__(
            entry,
            "data",
            MappingProxyType(
                {**entry.data, "token": {**old_token, "expires_at": time.time() - 1}}
            ),
        )
        fresh_token = {**old_token, "expires_at": time.time() + 86400}
        refreshed = AsyncMock(return_value=fresh_token)
        with patch.object(
            owner.session.implementation, "async_refresh_token", refreshed
        ):
            await asyncio.gather(
                owner.session.async_ensure_token_valid(),
                owner.session.async_ensure_token_valid(),
            )
        await self.hass.async_block_till_done()
        refreshed.assert_awaited_once()
        self.assertIs(entry.runtime_data, owner)
        self.assertTrue(owner.active)
        self.assertFalse(self._writes())

    async def test_failed_options_unload_does_not_mark_configuration_applied(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        baseline = owner.configuration_identity
        with patch.object(
            self.hass.config_entries,
            "async_unload_platforms",
            AsyncMock(return_value=False),
        ):
            self.hass.config_entries.async_update_entry(
                entry, options={**entry.options, "fixture_revision": 2}
            )
            await self.hass.async_block_till_done()
        self.assertIs(entry.runtime_data, owner)
        self.assertTrue(owner.active)
        self.assertEqual(owner.configuration_identity, baseline)
        self.assertNotEqual(
            owner.configuration_identity,
            fixture.podconnect._configuration_identity(entry),
        )
        # HA keeps its failed-unload state; this guard must not claim new config applied.
        self.assertFalse(self._writes())

    async def test_configuration_change_during_initial_refresh_is_not_published(self):
        from homeassistant.config_entries import ConfigEntryState

        from custom_components.podconnect.coordinator import PodConnectCoordinator

        entry = self.entries[0]
        old = entry.runtime_data
        entered, release = asyncio.Event(), asyncio.Event()
        self.holds.append(release)
        refresh = PodConnectCoordinator.async_config_entry_first_refresh

        async def held_refresh(coordinator):
            entered.set()
            await release.wait()
            await refresh(coordinator)

        with patch.object(
            PodConnectCoordinator, "async_config_entry_first_refresh", held_refresh
        ):
            reloading = self._task(
                self.hass.config_entries.async_reload(entry.entry_id)
            )
            await asyncio.wait_for(entered.wait(), 3)
            self.hass.config_entries.async_update_entry(
                entry, data={**entry.data, "account_identity": "changed-during-setup"}
            )
            release.set()
            self.assertFalse(await reloading)
        self.assertIs(entry.state, ConfigEntryState.SETUP_RETRY)
        self.assertFalse(old.active)
        self.assertFalse(getattr(getattr(entry, "runtime_data", None), "active", False))
        self.assertFalse(self._writes())

    async def test_queued_listener_without_runtime_cannot_reload(self):
        entry = self.entries[0]
        owner = entry.runtime_data
        del entry.runtime_data
        try:
            with patch.object(
                self.hass.config_entries, "async_reload", AsyncMock()
            ) as reload:
                await fixture.podconnect._options_updated(self.hass, entry)
                reload.assert_not_awaited()
        finally:
            entry.runtime_data = owner

    async def test_token_refresh_and_real_options_update_cannot_lose_configuration(
        self,
    ):
        entry = self.entries[0]
        owner = entry.runtime_data
        old_token = dict(entry.data["token"])
        object.__setattr__(
            entry,
            "data",
            MappingProxyType(
                {**entry.data, "token": {**old_token, "expires_at": time.time() - 1}}
            ),
        )
        with patch.object(
            owner.session.implementation,
            "async_refresh_token",
            AsyncMock(return_value={**old_token, "expires_at": time.time() + 86400}),
        ):
            await owner.session.async_ensure_token_valid()
            self.hass.config_entries.async_update_entry(
                entry, options={**entry.options, "fixture_revision": 2}
            )
            await self.hass.async_block_till_done()
        self.assertIsNot(entry.runtime_data, owner)
        self.assertFalse(owner.active)
        self.assertTrue(entry.runtime_data.active)
        self.assertEqual(
            entry.runtime_data.configuration_identity,
            fixture.podconnect._configuration_identity(entry),
        )
        self.assertFalse(self._writes())

    async def test_nested_configuration_snapshot_is_immutable_and_restorable(self):
        entry = self.entries[0]
        previous = entry.options
        nested = {"fixture": {"room": "original"}}
        object.__setattr__(entry, "options", MappingProxyType(nested))
        try:
            baseline = fixture.podconnect._configuration_identity(entry)
            nested["fixture"]["room"] = "replacement"
            self.assertNotEqual(
                baseline, fixture.podconnect._configuration_identity(entry)
            )
            nested["fixture"]["room"] = "original"
            self.assertEqual(
                baseline, fixture.podconnect._configuration_identity(entry)
            )
        finally:
            object.__setattr__(entry, "options", previous)
