#!/usr/bin/env python3
"""Host-safe negative contract checks; never supplies a fake kernel or cgroup."""

import os
import pathlib
import subprocess
import tempfile
import unittest

SCRIPT = pathlib.Path(__file__).with_name("delegate-entry.sh")


class DelegationRefusal(unittest.TestCase):
    def test_shell_syntax(self):
        subprocess.run(["/bin/sh", "-n", str(SCRIPT)], check=True)

    def refuse(self, scope, command, expected):
        env = dict(os.environ)
        env.pop("PC_RADIO_CHAIN_CGROUP_PARENT", None)
        env.pop("PODCONNECT_RADIO_DELEGATION_SCOPE", None)
        if scope is not None:
            env["PODCONNECT_RADIO_DELEGATION_SCOPE"] = scope
        result = subprocess.run(
            ["/bin/sh", str(SCRIPT), *command],
            env=env,
            capture_output=True,
            text=True,
            timeout=5,
            check=False,
        )
        self.assertEqual(result.returncode, 125, result.stderr)
        self.assertIn(expected, result.stderr)

    def test_missing_command(self):
        self.refuse("/pv-radio-negative", [], "original entrypoint required")

    def test_missing_shared_or_malformed_scope(self):
        for scope in (
            None,
            "/",
            "/sys/fs/cgroup",
            "/pv-radio-test/../shared",
            "/pv-radio-bad name",
        ):
            with self.subTest(scope=scope):
                self.refuse(scope, ["/bin/true"], "scope")

    def test_normal_host_cannot_exec_or_modify_original_data(self):
        with tempfile.TemporaryDirectory() as directory:
            original = pathlib.Path(directory, "spotify-options")
            original.write_bytes(b"original Spotify fixture remains unchanged\n")
            before = original.stat()
            self.refuse(
                "/pv-radio-negative",
                ["/bin/sh", "-c", 'echo changed > "$1"', "sh", str(original)],
                "must be initial PID1",
            )
            after = original.stat()
            self.assertEqual(
                original.read_bytes(), b"original Spotify fixture remains unchanged\n"
            )
            self.assertEqual(
                (before.st_ino, before.st_mode, before.st_uid, before.st_gid),
                (after.st_ino, after.st_mode, after.st_uid, after.st_gid),
            )

    def test_mount_policy_rejects_wrong_scope_and_shared_roots(self):
        # Exercise the exact production awk expression as a pure parser. These
        # strings are policy fixtures, never substitute kernel evidence in the
        # wrapper or claim successful real container execution.
        source = SCRIPT.read_text()
        policy = source.split('awk -v destination="$delegation" \'\n', 1)[1].split(
            "\n' /proc/1/mountinfo", 1
        )[0]
        readonly = "178 177 0:39 / /sys/fs/cgroup ro,nosuid - cgroup2 cgroup rw\n"
        delegated = "181 172 0:39 /.. /run/task-cgroup/pv-radio-exact rw,nosuid - cgroup2 cgroup2 rw\n"
        cases = (
            (readonly + delegated, "/run/task-cgroup/pv-radio-exact", 0),
            (readonly + delegated, "/run/task-cgroup/pv-radio-wrong", 1),
            (
                readonly + delegated.replace(" /.. ", " / "),
                "/run/task-cgroup/pv-radio-exact",
                1,
            ),
            (
                readonly
                + delegated
                + delegated.replace("/run/task-cgroup/pv-radio-exact", "/shared"),
                "/run/task-cgroup/pv-radio-exact",
                1,
            ),
            (
                readonly.replace(" ro,", " rw,") + delegated,
                "/run/task-cgroup/pv-radio-exact",
                1,
            ),
        )
        for evidence, destination, expected in cases:
            with self.subTest(destination=destination, evidence=evidence):
                result = subprocess.run(
                    ["awk", "-v", "destination=" + destination, policy],
                    input=evidence,
                    check=False,
                    text=True,
                    capture_output=True,
                    timeout=5,
                )
                self.assertEqual(result.returncode, expected, result.stderr)


if __name__ == "__main__":
    unittest.main()
