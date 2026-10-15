"""Pure diagnostic contract tests; no HA/provider/credentials or play path."""

import copy
import hashlib
import importlib.util
from pathlib import Path
import unittest

FILE = Path(__file__).parents[1] / "custom_components/podconnect/account_diagnostic.py"
SPEC = importlib.util.spec_from_file_location("account_diagnostic", FILE)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
COMPARE = MODULE.compare_account_identifiers
ENGINE = "a" * 40
NOW = 100000


def identity(value="exact-fixture-id"):
    return {"schema": "podconnect.start_identity.v1", "engine_device_id": ENGINE,
            "account_observation": {
                "schema": "podconnect.native_account_observation.v1", "method": "sha256",
                "domain": "spotify.ap.canonical_username",
                "identifier_sha256": hashlib.sha256(value.encode()).hexdigest(),
                "engine_device_id": ENGINE, "run_generation": "1",
                "connection_generation": "2", "observation_sequence": "3",
                "observed_at_unix_ms": NOW - 1, "expires_at_unix_ms": NOW + 4999}}


class AccountDiagnosticTest(unittest.TestCase):
    def compare(self, doc=None, account="exact-fixture-id", engine=ENGINE, now=NOW):
        return COMPARE(identity() if doc is None else doc, account, engine, now_ms=now)

    def test_equal_is_only_identifier_equality_not_authority(self):
        self.assertEqual(self.compare(), {"comparison": "identifiers_equal", "identity_relation": "unknown"})

    def test_different_is_not_proven_account_mismatch(self):
        self.assertEqual(self.compare(account="another-fixture-id"),
                         {"comparison": "identifiers_different", "identity_relation": "unknown"})

    def test_no_casefold_trim_or_display_name_guess(self):
        self.assertEqual(self.compare(account="Exact-fixture-id")["comparison"], "identifiers_different")
        for value in (" exact-fixture-id", "exact-fixture-id ", "", None, "a\n", "\ud800", "ø" * 600):
            with self.subTest(value=repr(value)):
                self.assertEqual(self.compare(account=value)["comparison"], "unavailable")

    def test_old_identity_and_unknown_optional_observation(self):
        for doc in ({"schema": "podconnect.start_identity.v1", "engine_device_id": ENGINE},
                    {**identity(), "account_observation": None}):
            self.assertEqual(self.compare(doc)["comparison"], "unavailable")

    def test_foreign_engine_or_schema_rejected(self):
        for path, value in (("engine_device_id", "b" * 40), ("schema", "foreign")):
            doc = identity()
            doc[path] = value
            self.assertEqual(self.compare(doc)["comparison"], "unavailable")
        doc = identity()
        doc["account_observation"]["engine_device_id"] = "b" * 40
        self.assertEqual(self.compare(doc)["comparison"], "unavailable")

    def test_closed_contract_and_generation_types(self):
        for key, value in (("method", "sha1"), ("domain", "spotify.display_name"),
                           ("identifier_sha256", "A" * 64), ("run_generation", "0"),
                           ("connection_generation", "02"), ("observation_sequence", True),
                           ("extra", "unknown")):
            doc = identity()
            doc["account_observation"][key] = value
            self.assertEqual(self.compare(doc)["comparison"], "unavailable", key)

    def test_stale_future_invalid_ttl_and_bool_timestamp_rejected(self):
        for observed, expires in ((NOW - 5000, NOW), (NOW + 1, NOW + 5001),
                                  (NOW - 1, NOW + 5000), (True, 5001), (NOW - 1, 2**53)):
            doc = identity()
            doc["account_observation"].update(observed_at_unix_ms=observed, expires_at_unix_ms=expires)
            self.assertEqual(self.compare(doc)["comparison"], "unavailable")

    def test_no_output_identifier_digest_or_input_mutation(self):
        doc = identity()
        before = copy.deepcopy(doc)
        result = self.compare(doc)
        self.assertEqual(doc, before)
        self.assertEqual(set(result), {"comparison", "identity_relation"})
        self.assertNotIn("exact-fixture-id", repr(result))
        self.assertNotIn(doc["account_observation"]["identifier_sha256"], repr(result))


if __name__ == "__main__":
    unittest.main()
