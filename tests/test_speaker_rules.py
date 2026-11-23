"""The family's speaker rules: decisions and PodVoice policy import, without HA."""

import hashlib
import importlib
import json
import sys
import types
import unittest
from pathlib import Path

package = types.ModuleType("podconnect_speaker_rules")
package.__path__ = [str(Path(__file__).parents[1] / "custom_components/podconnect")]
sys.modules[package.__name__] = package
rules = importlib.import_module(package.__name__ + ".speaker_rules")

ENTRY = "account-alpha"


def policy(*music):
    return json.dumps({"roomId": "screen", "music": list(music), "vacuum": []})


def row(room="room-alpha", actions=("pause", "volume"), limit=40, account=ENTRY, **extra):
    value = {
        "accountId": account,
        "targetId": "spotify-device",
        "actions": list(actions),
        "maxVolume": limit,
    }
    if room is not None:
        value.update(nativeRoomId=room, nativeHomepodId="homepod-" + room)
    value.update(extra)
    return value


class PodVoicePolicyImportTests(unittest.TestCase):
    def test_room_rows_of_this_account_become_room_rules(self):
        imported, ignored = rules.from_podview_policy(
            policy(
                row(actions=["pause", "resume", "volume"], limit=40, default=True),
                row(room="room-beta", actions=["play_track"], limit=25),
                row(room="room-gamma", account="account-beta"),
                row(room=None),
            ),
            ENTRY,
        )
        self.assertEqual(ignored, 2)
        self.assertEqual(
            imported,
            {
                "rooms": {
                    # PodVoice: a rule that may pause or resume may also seek.
                    "room-alpha": {
                        "actions": ["pause", "resume", "seek", "volume"],
                        "max_volume": 40,
                    },
                    "room-beta": {"actions": ["play_track"], "max_volume": 25},
                },
                "other_account_guard": True,
                "unlisted_rooms": "allow",
            },
        )

    def test_malformed_policy_grants_nothing_and_is_refused(self):
        for bad in (
            "not json",
            json.dumps([]),
            policy(row(limit=101)),
            policy(row(limit=True)),
            policy(row(actions=[])),
            policy(row(actions=["pause", "pause"])),
            policy(row(actions=["shuffle"])),
            policy({**row(), "surprise": 1}),
            policy(row(), row()),  # two rules for one room: PodVoice grants nothing
            policy(*[row(room=f"r{i}") for i in range(9)]),
            "x" * 8193,
        ):
            with self.subTest(bad=bad[:40]), self.assertRaises(rules.RulesInvalid):
                rules.from_podview_policy(bad, ENTRY)

    def test_stored_rules_are_closed(self):
        self.assertIsNone(rules.normalize(None))
        good = {"rooms": {"r": {"actions": ["volume"], "max_volume": 0}}}
        self.assertEqual(
            rules.normalize(good),
            {**good, "other_account_guard": True, "unlisted_rooms": "allow"},
        )
        for bad in (
            [],
            {"rooms": {}, "extra": 1},
            {"rooms": {"r": {"actions": ["volume"]}}},
            {"rooms": {"r": {"actions": ["fly"], "max_volume": 1}}},
            {"rooms": {"r": {"actions": [], "max_volume": 1.5}}},
            {"rooms": {"": {"actions": [], "max_volume": 1}}},
            {"other_account_guard": "yes"},
            {"unlisted_rooms": "maybe"},
        ):
            with self.subTest(bad=bad), self.assertRaises(rules.RulesInvalid):
                rules.normalize(bad)


class DecisionTests(unittest.TestCase):
    def setUp(self):
        self.rules = rules.normalize(
            {
                "rooms": {
                    "alpha": {"actions": ["pause", "resume", "seek", "volume"], "max_volume": 40},
                    "beta": {"actions": ["play_track", "play_context"], "max_volume": 25},
                    "gamma": {"actions": ["move_existing_session"], "max_volume": 50},
                }
            }
        )

    def test_no_rules_allow_everything_unchanged(self):
        for action in ("volume", "pause", "play_track", "move_existing_session"):
            self.assertIsNone(rules.decide(None, "alpha", action, account="other"))
        self.assertEqual(rules.capped(None, "alpha", 90), 90)

    def test_each_action_needs_the_room_rule(self):
        allowed = {
            "alpha": {"pause", "resume", "seek", "volume", "move_existing_session"},
            "beta": {"play_track", "play_context"},
            "gamma": {"move_existing_session"},
        }
        for room, granted in allowed.items():
            for action in sorted(rules.RULE_ACTIONS):
                with self.subTest(room=room, action=action):
                    want = None if action in granted else rules.DENIED
                    self.assertEqual(rules.decide(self.rules, room, action), want)

    def test_podconnect_action_names_map_to_podvoice_names(self):
        self.assertIsNone(rules.decide(self.rules, "beta", "play_episode"))
        self.assertIsNone(rules.decide(self.rules, "beta", "play_tracks"))
        self.assertEqual(rules.decide(self.rules, "beta", "play"), rules.DENIED)
        # Shuffle and repeat are not part of today's rules.
        self.assertIsNone(rules.decide(self.rules, "beta", "shuffle"))
        self.assertIsNone(rules.decide(self.rules, "beta", "repeat"))

    def test_volume_is_lowered_to_the_ceiling_like_podvoice(self):
        self.assertEqual(rules.capped(self.rules, "alpha", 90), 40)
        self.assertEqual(rules.capped(self.rules, "alpha", 40), 40)
        self.assertEqual(rules.capped(self.rules, "alpha", 12), 12)
        self.assertEqual(rules.capped(self.rules, "unlisted", 90), 90)

    def test_other_account_blocks_steering_but_not_starting_own_music(self):
        for action in rules.PLAYER_ACTIONS:
            want = rules.OTHER_ACCOUNT if action in {"pause", "resume", "seek", "volume"} else rules.DENIED
            self.assertEqual(rules.decide(self.rules, "alpha", action, account="other"), want)
            self.assertIn(
                rules.decide(self.rules, "alpha", action, account="own"), (None, rules.DENIED)
            )
        self.assertIsNone(rules.decide(self.rules, "beta", "play_track", account="other"))
        off = {**self.rules, "other_account_guard": False}
        self.assertIsNone(rules.decide(off, "alpha", "pause", account="other"))

    def test_unlisted_and_unknown_rooms(self):
        self.assertIsNone(rules.decide(self.rules, "delta", "volume"))
        deny = {**self.rules, "unlisted_rooms": "deny"}
        self.assertEqual(rules.decide(deny, "delta", "volume"), rules.DENIED)
        self.assertEqual(rules.decide(self.rules, None, "pause"), rules.ROOM_UNKNOWN)
        self.assertIsNone(rules.decide(self.rules, None, "shuffle"))
        empty = rules.normalize({"rooms": {}})
        self.assertIsNone(rules.decide(empty, None, "pause"))

    def test_malformed_stored_rules_deny(self):
        entry = types.SimpleNamespace(options={rules.OPTION: {"rooms": "broken"}})
        self.assertEqual(rules.decide(rules.rules_of(entry), "alpha", "pause"), rules.DENIED)
        self.assertIsNone(rules.rules_of(types.SimpleNamespace(options={})))

    def test_room_account_is_a_digest_comparison(self):
        mine = hashlib.sha256(b"me").hexdigest()
        self.assertEqual(rules.room_account({"account_sha256": mine}, "me"), "own")
        self.assertEqual(rules.room_account({"account_sha256": "0" * 64}, "me"), "other")
        self.assertEqual(rules.room_account({}, "me"), "unknown")
        self.assertEqual(rules.room_account({"account_sha256": mine}, None), "unknown")


if __name__ == "__main__":
    unittest.main()
