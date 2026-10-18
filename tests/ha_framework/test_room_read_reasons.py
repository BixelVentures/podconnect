"""Actual HA read service preserves only the closed native diagnostic enum."""

import json

from aiohttp import web
from test_room_transport import RoomTransportFrameworkTests


class RoomReadReasonTests(RoomTransportFrameworkTests):
    async def _backend(self, request):
        if request.path == "/api/room-transport/state" and hasattr(
            self, "diagnostic_body"
        ):
            self.room_reads.append(dict(request.query))
            return web.Response(
                status=409, text=self.diagnostic_body, content_type="application/json"
            )
        return await super()._backend(request)

    async def test_closed_reasons_survive_actual_service(self):
        data = self.entries[0].runtime_data
        before = (data.coordinator.poll_sequence, data.commands.command_sequence)
        for reason in [
            "claim_unavailable",
            "receiver_proof_unavailable",
            "output_proof_unavailable",
            "route_owner_unavailable",
        ]:
            self.diagnostic_body = json.dumps(
                {
                    "ok": False,
                    "error_kind": "room_transport_unavailable",
                    "retry_allowed": False,
                    "reason": reason,
                }
            )
            result = await self.room_offer()
            self.assertEqual(
                result,
                {
                    "ok": False,
                    "error_kind": "room_transport_unavailable",
                    "retry_allowed": False,
                    "reason": reason,
                },
            )
        self.assertEqual(
            before, (data.coordinator.poll_sequence, data.commands.command_sequence)
        )
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self._writes(), [])

    async def test_malformed_reasons_remain_closed(self):
        baseline = {
            "ok": False,
            "error_kind": "room_transport_unavailable",
            "retry_allowed": False,
            "reason": "claim_unavailable",
        }
        values = [
            dict(baseline, reason="private token"),
            dict(baseline, reason=["claim_unavailable"]),
            dict(baseline, retry_allowed=True),
            dict(baseline, ok=True),
            dict(baseline, private="secret"),
            {k: v for k, v in baseline.items() if k != "reason"},
        ]
        bodies = [json.dumps(v) for v in values] + [
            '{"reason":"claim_unavailable","reason":"secret"}',
            "private token",
            "x" * 65537,
        ]
        for body in bodies:
            self.diagnostic_body = body
            result = await self.room_offer()
            self.assertEqual(
                result,
                {
                    "ok": False,
                    "error_kind": "room_transport_unavailable",
                    "retry_allowed": False,
                },
            )
        self.assertEqual(self.room_posts, [])
        self.assertEqual(self._writes(), [])
