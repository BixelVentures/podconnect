"""Pure shipped-module public contract tests; no HA/provider/service registration."""

import copy
import importlib.util
import unittest
from pathlib import Path

SOURCE = Path(__file__).parents[1] / "custom_components/podconnect/room_content.py"
spec = importlib.util.spec_from_file_location("room_content_contract", SOURCE)
contract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(contract)


class SelectedContentContractTests(unittest.TestCase):
    def offer(self):
        return {
            "schema": "podconnect.room_content_offer.v1",
            "offer_id": "a" * 32,
            "binding_id": "b" * 64,
            "native_incarnation": "c" * 32,
            "policy_revision": "d" * 64,
            "start_volume_percent": 35,
            "expires_after_ms": 5000,
        }

    def receipt(self):
        identity = {
            "config_entry_id": "account-a",
            "owner_epoch": "epoch-a",
            "request_id": "epoch-a:1",
            "command_sequence": 1,
            "action": "play_context",
            "target": {"kind": "spotify_device", "target_id": "native-engine"},
            "room_id": "explicit-owned-room-b",
            "homepod_id": "456",
        }
        value = {
            **copy.deepcopy(identity),
            "ok": True,
            "schema": "podconnect.command.v1",
            "outcome": "accepted",
            "provider_request_accepted": False,
            "retry_allowed": False,
            "physical_result_verified": False,
            "accepted_selected_content": True,
            "start_operation_id": "e" * 32,
            "start_volume_percent": 35,
        }
        return value, identity

    def test_closed_read_offer_is_descriptor_not_generic_session_authority(self):
        self.assertTrue(contract.valid_start_offer(self.offer()))
        for key, value in (
            ("offer_id", "A" * 32),
            ("binding_id", "b" * 63),
            ("native_incarnation", ""),
            ("policy_revision", True),
            ("start_volume_percent", 27),
            ("expires_after_ms", True),
            ("expires_after_ms", 5001),
            ("expires_after_ms", 0),
        ):
            self.assertFalse(contract.valid_start_offer({**self.offer(), key: value}))
        self.assertFalse(
            contract.valid_start_offer({**self.offer(), "session_id": "invented"})
        )
        for key in self.offer():
            value = self.offer()
            del value[key]
            self.assertFalse(contract.valid_start_offer(value))

    def test_selected_source_accepts_exact_track_or_playlist_with_authorized_ceiling(
        self,
    ):
        self.assertTrue(
            contract.valid_selected_arguments(
                "play_track",
                {"track_uri": "spotify:track:" + "A" * 22, "max_volume": 35},
            )
        )
        self.assertTrue(
            contract.valid_selected_arguments(
                "play_context",
                {"context_uri": "spotify:playlist:" + "B" * 22, "max_volume": 100},
            )
        )
        for percent in (0, 27, 34, True, 35.0, float("nan"), 101):
            self.assertFalse(
                contract.valid_selected_arguments(
                    "play_track",
                    {"track_uri": "spotify:track:" + "A" * 22, "max_volume": percent},
                )
            )
        self.assertFalse(contract.valid_selected_arguments("resume", {}))
        self.assertFalse(
            contract.valid_selected_arguments(
                "play_track",
                {"track_uri": "spotify:playlist:" + "A" * 22, "max_volume": 35},
            )
        )
        self.assertFalse(
            contract.valid_selected_arguments(
                "play_context",
                {
                    "context_uri": "spotify:playlist:" + "B" * 22,
                    "max_volume": 35,
                    "offset": 0,
                },
            )
        )

    def test_only_complete_native_flag_accepts_exact_authorized_destination(self):
        value, identity = self.receipt()
        self.assertTrue(contract.valid_selected_receipt(value, identity))
        for key, replacement in (
            ("accepted_selected_content", False),
            ("start_operation_id", None),
            ("start_volume_percent", None),
            ("room_id", "default-kitchen"),
            ("homepod_id", "other"),
            ("provider_request_accepted", True),
            ("physical_result_verified", True),
            ("retry_allowed", True),
        ):
            self.assertFalse(
                contract.valid_selected_receipt({**value, key: replacement}, identity)
            )
        self.assertFalse(
            contract.valid_selected_receipt(
                {**value, "accepted_native": True}, identity
            )
        )
        self.assertFalse(
            contract.valid_selected_receipt(
                {**value, "protocol_accepted": True}, identity
            )
        )
        self.assertFalse(
            contract.valid_selected_receipt(
                {**value, "start_operation_id": "E" * 32}, identity
            )
        )

    def test_unknown_nulls_do_not_masquerade_as_success_and_early_reject_is_closed(
        self,
    ):
        value, identity = self.receipt()
        for outcome in ("unknown", "rejected"):
            reply = {
                **value,
                "ok": False,
                "outcome": outcome,
                "accepted_selected_content": False,
                "start_operation_id": None,
                "start_volume_percent": None,
                "error_kind": "outcome_unknown"
                if outcome == "unknown"
                else "unavailable",
                "observe_after_poll_sequence": 0,
            }
            self.assertTrue(contract.valid_selected_receipt(reply, identity))
            self.assertFalse(
                contract.valid_selected_receipt(
                    {**reply, "observe_after_poll_sequence": True}, identity
                )
            )
            self.assertFalse(
                contract.valid_selected_receipt(
                    {**reply, "possible_effect": False}, identity
                )
            )
        self.assertFalse(
            contract.valid_selected_receipt({**value, "extra": True}, identity)
        )
        for key in value:
            reply = dict(value)
            del reply[key]
            self.assertFalse(contract.valid_selected_receipt(reply, identity))


if __name__ == "__main__":
    unittest.main()
