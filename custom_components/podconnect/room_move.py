"""Retained owned-session MOVE through the shared canonical preparation seam."""

from .room_content import (
    _HEX32,
    _HEX64,
    RoomContent,
    _hex,
    valid_manager_result,
    valid_selected_receipt,
)


def valid_move_offer(value):
    return (
        isinstance(value, dict)
        and set(value)
        == {
            "schema",
            "offer_id",
            "binding_id",
            "native_incarnation",
            "policy_revision",
            "session_volume_percent",
            "expires_after_ms",
        }
        and value["schema"] == "podconnect.room_output_move_offer.v1"
        and all(_hex(value[k], _HEX32) for k in ("offer_id", "native_incarnation"))
        and all(_hex(value[k], _HEX64) for k in ("binding_id", "policy_revision"))
        and type(value["session_volume_percent"]) is int
        and 0 <= value["session_volume_percent"] <= 100
        and type(value["expires_after_ms"]) is int
        and 1 <= value["expires_after_ms"] <= 5000
    )


def valid_move_arguments(action, value):
    return action == "move_existing_session" and isinstance(value, dict) and not value


def valid_move_receipt(value, identity):
    return valid_selected_receipt(
        value,
        identity,
        accepted_key="accepted_room_move",
        operation_key="move_operation_id",
        volume_key="session_volume_percent",
        actions=("move_existing_session",),
        volume_valid=lambda volume: type(volume) is int and 0 <= volume <= 100,
    )


def valid_move_result(value, request, suffix):
    return valid_manager_result(
        value,
        request,
        suffix,
        namespace="room_move",
        accepted_key="accepted_room_move",
        operation_key="move_operation_id",
        volume_key="session_volume_percent",
        offer_key="move_offer",
        expected_volume=request["move_offer"]["session_volume_percent"],
    )


class RoomMove(RoomContent):
    """Only the purpose/wire differ; highwater and execution owner remain shared."""

    namespace = "room_move"
    offer_key = "move_offer"
    accepted_key = "accepted_room_move"
    operation_key = "move_operation_id"
    volume_key = "session_volume_percent"
    actions = ("move_existing_session",)
    staging_timeout = 8
    valid_offer = staticmethod(valid_move_offer)
    valid_arguments = staticmethod(valid_move_arguments)
    valid_result = staticmethod(valid_move_result)

    def expected_volume(self, request):
        return request["move_offer"]["session_volume_percent"]
