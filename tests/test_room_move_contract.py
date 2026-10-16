"""Closed MOVE schema validation from shipped sources, without HA or effects."""

import copy
import importlib
import sys
import types
import unittest
from pathlib import Path

package = types.ModuleType("podconnect_move_contract")
package.__path__ = [str(Path(__file__).parents[1] / "custom_components/podconnect")]
sys.modules[package.__name__] = package
contract = importlib.import_module(package.__name__ + ".room_move")


class MoveContractTests(unittest.TestCase):
    def offer(self):
        return {
            "schema": "podconnect.room_output_move_offer.v1",
            "offer_id": "a" * 32,
            "binding_id": "b" * 64,
            "native_incarnation": "c" * 32,
            "policy_revision": "d" * 64,
            "session_volume_percent": 27,
            "expires_after_ms": 5000,
        }

    def test_offer_is_closed_with_actual_server_percent_and_no_caller_args(self):
        for percent in (0, 27, 100):
            self.assertTrue(
                contract.valid_move_offer(
                    {**self.offer(), "session_volume_percent": percent}
                )
            )
        for key, value in (
            ("session_volume_percent", True),
            ("session_volume_percent", 27.0),
            ("session_volume_percent", 101),
            ("expires_after_ms", 0),
            ("expires_after_ms", 5001),
            ("offer_id", "A" * 32),
        ):
            self.assertFalse(contract.valid_move_offer({**self.offer(), key: value}))
        self.assertFalse(
            contract.valid_move_offer({**self.offer(), "session_id": "caller"})
        )
        self.assertTrue(contract.valid_move_arguments("move_existing_session", {}))
        for args in (
            {"volume_percent": 27},
            {"max_volume": 35},
            {"context_uri": "new-source"},
            None,
        ):
            self.assertFalse(
                contract.valid_move_arguments("move_existing_session", args)
            )
        self.assertFalse(contract.valid_move_arguments("play_context", {}))

    def test_complete_requires_exact_offer_percent_and_only_joined_complete_flag(self):
        request = {
            "move_offer": self.offer(),
            "room_id": "chosen-room",
            "homepod_id": "456",
        }
        result = {
            "schema": "podconnect.room_move_commit.v1",
            "ok": True,
            "offer_id": "a" * 32,
            "binding_id": "b" * 64,
            "state": "complete",
            "accepted_room_move": True,
            "move_operation_id": "a" * 32,
            "session_volume_percent": 27,
            "room_id": "chosen-room",
            "homepod_id": "456",
            "physical_result_verified": False,
        }
        self.assertTrue(contract.valid_move_result(result, request, "commit"))
        for key, value in (
            ("session_volume_percent", 35),
            ("session_volume_percent", 60),
            ("session_volume_percent", True),
            ("move_operation_id", "f" * 32),
            ("accepted_room_move", False),
            ("room_id", "default-kitchen"),
            ("physical_result_verified", True),
        ):
            self.assertFalse(
                contract.valid_move_result({**result, key: value}, request, "commit")
            )
        self.assertFalse(
            contract.valid_move_result(
                {**result, "accepted_native": True}, request, "commit"
            )
        )
        unknown = {
            **result,
            "state": "unknown",
            "ok": False,
            "accepted_room_move": False,
            "move_operation_id": None,
            "session_volume_percent": None,
        }
        self.assertTrue(contract.valid_move_result(unknown, request, "commit"))
        self.assertFalse(
            contract.valid_move_result({**unknown, "ok": True}, request, "commit")
        )

    def test_public_complete_uses_same_canonical_shape_without_start_authority(self):
        identity = {
            "config_entry_id": "account",
            "owner_epoch": "owner",
            "request_id": "owner:1",
            "command_sequence": 1,
            "action": "move_existing_session",
            "target": {"kind": "spotify_device", "target_id": "same-native-engine"},
            "room_id": "chosen-room",
            "homepod_id": "456",
        }
        receipt = {
            **copy.deepcopy(identity),
            "schema": "podconnect.command.v1",
            "ok": True,
            "outcome": "accepted",
            "provider_request_accepted": False,
            "retry_allowed": False,
            "physical_result_verified": False,
            "accepted_room_move": True,
            "move_operation_id": "a" * 32,
            "session_volume_percent": 27,
        }
        self.assertTrue(contract.valid_move_receipt(receipt, identity))
        for key, value in (
            ("accepted_room_move", False),
            ("session_volume_percent", None),
            ("move_operation_id", None),
            ("provider_request_accepted", True),
            ("retry_allowed", True),
            ("physical_result_verified", True),
        ):
            self.assertFalse(
                contract.valid_move_receipt({**receipt, key: value}, identity)
            )
        self.assertFalse(
            contract.valid_move_receipt(
                {**receipt, "accepted_selected_content": True}, identity
            )
        )
