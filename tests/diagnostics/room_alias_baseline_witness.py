"""Expected unsafe baseline witness, run explicitly against ORIGINAL Control 14.

This preserves the generic service's semantics; it is not an acceptance test or
proof of real speaker delivery. Only the fixture's inert Spotify endpoint sees a
write. Execute with the original repository and HA framework fixture on PYTHONPATH.
"""

import asyncio
import json
import unittest
from unittest.mock import patch

from test_commands import CommandFrameworkTests


class LegacyRoomAliasWitness(CommandFrameworkTests):
    async def witness_same_engine_alias_change_is_not_a_generic_command_guard(self):
        data = self.entries[0].runtime_data
        original = data.session.async_ensure_token_valid
        entered, release = asyncio.Event(), asyncio.Event()
        self.holds.append(release)

        async def held():
            await original()
            entered.set()
            await release.wait()

        request = self.request("resume")
        epoch, poll, account = (
            data.coordinator.owner_epoch,
            data.coordinator.poll_sequence,
            self.entries[0].unique_id,
        )
        with patch.object(data.session, "async_ensure_token_valid", held):
            task = self._task(self.invoke(request))
            await asyncio.wait_for(entered.wait(), 2)
            self.native_source_room = "room-beta"
            self.assertEqual(
                (
                    data.coordinator.owner_epoch,
                    data.coordinator.poll_sequence,
                    self.entries[0].unique_id,
                ),
                (epoch, poll, account),
            )
            release.set()
            result = await task
        self.assertEqual(result["outcome"], "accepted")
        self.assertEqual(len(self._writes()), 1)
        self.assertEqual((self.native_prepares, self.native_commits), (0, 0))
        self.assertFalse(any(call[1].startswith("/api/") for call in self.calls))
        print(
            json.dumps(
                {
                    "witness": "expected_unsafe_legacy_room_alias_gap",
                    "same_account_engine_owner_poll": True,
                    "alias_changed": "room-alpha_to_room-beta",
                    "legacy_outcome": result["outcome"],
                    "inert_spotify_writes": len(self._writes()),
                    "native_checks": 0,
                    "physical_result_verified": False,
                }
            ),
            flush=True,
        )


if __name__ == "__main__":
    suite = unittest.TestSuite(
        [
            LegacyRoomAliasWitness(
                "witness_same_engine_alias_change_is_not_a_generic_command_guard"
            )
        ]
    )
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    raise SystemExit(not result.wasSuccessful())
