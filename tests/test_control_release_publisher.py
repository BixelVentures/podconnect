from email.message import Message
from io import BytesIO
from urllib.response import addinfourl

"""Inert exact-version publisher fixtures: no token, network or repository mutation."""

import importlib.util
import pathlib
import unittest

SCRIPT = pathlib.Path(__file__).parents[1] / "scripts/publish_control_release.py"
spec = importlib.util.spec_from_file_location("control_release", SCRIPT)
publisher = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publisher)
S, OLD = "a" * 40, "b" * 40
NAME = "0.10.20"


class FakeAPI:
    def __init__(self, target=None, release=None):
        self.target, self.release = target, release
        self.main, self.calls = S, []
        self.fail = None

    def __call__(self, method, path, body=None):
        self.calls.append((method, path, body))
        if self.fail == (method, path):
            raise publisher.Rejected("unknown API failure")
        if method == "GET" and path == "git/ref/heads/main":
            return 200, {
                "ref": "refs/heads/main",
                "object": {"type": "commit", "sha": self.main},
            }
        if method == "GET" and path == f"releases/tags/{NAME}":
            return (404, None) if self.release is None else (200, self.release)
        if method == "GET" and path == f"git/ref/tags/{NAME}":
            return (
                (404, None)
                if self.target is None
                else (
                    200,
                    {
                        "ref": f"refs/tags/{NAME}",
                        "object": {"type": "commit", "sha": self.target},
                    },
                )
            )
        if method == "POST" and path == "git/refs":
            self.target = body["sha"]
            return 201, {"ref": body["ref"]}
        if method == "POST" and path == "releases":
            self.release = dict(body)
            return 201, self.release
        raise AssertionError((method, path))


class PublisherTests(unittest.TestCase):
    def test_candidate_exact_checkout_version_change_and_no_change(self):
        env = {
            "GITHUB_EVENT_NAME": "push",
            "GITHUB_REF": "refs/heads/main",
            "GITHUB_REPOSITORY": publisher.REPOSITORY,
            "GITHUB_SHA": S,
            "RELEASE_BEFORE": OLD,
        }

        def run(*args):
            if args == ("rev-parse", "HEAD"):
                return S
            if args[0] == "show":
                return (
                    '{"version":"'
                    + ("0.10.19" if args[1].startswith(OLD) else NAME)
                    + '"}'
                )
            self.assertEqual(args, ("merge-base", "--is-ancestor", OLD, S))

        self.assertEqual(publisher.candidate(env, run), (NAME, S))
        self.assertIsNone(
            publisher.candidate(
                env,
                lambda *args: (
                    S
                    if args[0] == "rev-parse"
                    else '{"version":"0.10.20"}'
                    if args[0] == "show"
                    else None
                ),
            )
        )
        for key, value in [
            ("GITHUB_EVENT_NAME", "pull_request"),
            ("GITHUB_REF", "refs/heads/other"),
            ("GITHUB_REPOSITORY", "other/repo"),
            ("RELEASE_BEFORE", "0" * 40),
            ("GITHUB_SHA", "main"),
        ]:
            with self.subTest(key=key), self.assertRaises(publisher.Rejected):
                publisher.candidate({**env, key: value}, run)
        with self.assertRaises(publisher.Rejected):
            publisher.candidate(env, lambda *args: OLD)

    def test_strict_semver(self):
        for good in [NAME, "1.0.0", "0.1.0-rc.1+build.2"]:
            self.assertEqual(publisher.version(good), good)
        for bad in [
            "0.0.0",
            "v0.10.20",
            "01.2.3",
            "1.2",
            "1.2.3\n",
            "1.2.3/a",
            "1.2.3-01",
            "1.2.3+",
            None,
        ]:
            with self.subTest(value=bad), self.assertRaises(publisher.Rejected):
                publisher.version(bad)

    def test_publish_then_exact_idempotent_readback(self):
        api = FakeAPI()
        self.assertEqual(publisher.publish(api, NAME, S), "published")
        self.assertEqual(api.target, S)
        self.assertEqual(api.release["target_commitish"], S)
        api.calls.clear()
        self.assertEqual(publisher.publish(api, NAME, S), "already published")
        self.assertTrue(all(c[0] == "GET" for c in api.calls))

    def test_matching_preexisting_tag_can_publish(self):
        api = FakeAPI(target=S)
        publisher.publish(api, NAME, S)
        self.assertFalse(any(c[:2] == ("POST", "git/refs") for c in api.calls))

    def test_foreign_tag_or_release_never_overwritten(self):
        for api in [
            FakeAPI(target=OLD),
            FakeAPI(
                target=S,
                release={
                    "tag_name": NAME,
                    "target_commitish": OLD,
                    "draft": False,
                    "prerelease": False,
                },
            ),
            FakeAPI(
                release={
                    "tag_name": NAME,
                    "target_commitish": S,
                    "draft": False,
                    "prerelease": False,
                }
            ),
        ]:
            with self.assertRaises(publisher.Rejected):
                publisher.publish(api, NAME, S)
            self.assertTrue(all(c[0] == "GET" for c in api.calls))

    def test_stale_main_and_unknown_api_fail_closed_no_retry(self):
        api = FakeAPI()
        api.main = OLD
        with self.assertRaises(publisher.Rejected):
            publisher.publish(api, NAME, S)
        self.assertTrue(all(c[0] == "GET" for c in api.calls))
        for path in ["git/refs", "releases"]:
            api = FakeAPI()
            api.fail = ("POST", path)
            with self.assertRaises(publisher.Rejected):
                publisher.publish(api, NAME, S)
            self.assertEqual(sum(c[:2] == ("POST", path) for c in api.calls), 1)
            self.assertIsNone(api.release)

    def test_annotated_tag_exact_commit_and_unknown_object_fail_closed(self):
        def api(method, path, body=None):
            if path == f"git/ref/tags/{NAME}":
                return 200, {
                    "ref": f"refs/tags/{NAME}",
                    "object": {"type": "tag", "sha": OLD},
                }
            self.assertEqual(path, f"git/tags/{OLD}")
            return 200, {"sha": OLD, "object": {"type": "commit", "sha": S}}

        self.assertEqual(publisher.tag_target(api, NAME), S)
        with self.assertRaises(publisher.Rejected):
            publisher.tag_target(
                lambda *args: (
                    200,
                    {"ref": f"refs/tags/{NAME}", "object": {"type": "tree", "sha": S}},
                ),
                NAME,
            )

    def test_main_advance_after_tag_creation_stops_release(self):
        api = FakeAPI()

        def advancing(method, path, body=None):
            result = api(method, path, body)
            if (method, path) == ("POST", "git/refs"):
                api.main = OLD
            return result

        with self.assertRaises(publisher.Rejected):
            publisher.publish(advancing, NAME, S)
        self.assertEqual(api.target, S)
        self.assertIsNone(api.release)
        self.assertFalse(any(c[:2] == ("POST", "releases") for c in api.calls))

    def test_api_redirects_are_rejected_without_forwarding_or_retry(self):
        token = "inert-fixture-token"
        for code in [301, 302, 303, 307, 308]:
            with self.subTest(code=code):
                calls = []

                class InertHTTPS(publisher.urllib.request.BaseHandler):
                    handler_order = 100

                    def https_open(self, request, calls=calls, code=code):
                        calls.append(
                            (request.full_url, request.get_header("Authorization"))
                        )
                        headers = Message()
                        headers["Location"] = "https://foreign.invalid/token"
                        response = addinfourl(
                            BytesIO(b""), headers, request.full_url, code
                        )
                        response.msg = "fixture redirect"
                        return response

                api = publisher.API(token)
                api.opener.add_handler(InertHTTPS())
                with self.assertRaises(publisher.Rejected) as raised:
                    api("POST", "releases", {"tag_name": NAME})
                self.assertNotIn(token, str(raised.exception))
                self.assertNotIn("foreign", str(raised.exception))
                self.assertEqual(
                    calls,
                    [
                        (
                            "https://api.github.com/repos/BixelVentures/podconnect/releases",
                            f"Bearer {token}",
                        )
                    ],
                )

    def test_workflow_preserves_both_required_gates_and_write_scope(self):
        text = (
            SCRIPT.parents[1] / ".github/workflows/verify-ha-control.yaml"
        ).read_text()
        self.assertIn("needs: [boundary-tests, framework-tests]", text)
        self.assertIn("needs.boundary-tests.result == 'success'", text)
        self.assertIn("needs.framework-tests.result == 'success'", text)
        self.assertIn("github.event_name == 'push'", text)
        self.assertIn("ref: ${{ github.sha }}", text)
        self.assertEqual(text.count('      - "scripts/publish_control_release.py"'), 2)
        before, publish = text.split("  publish-control:")
        self.assertNotIn("contents: write", before)
        self.assertIn("contents: write", publish)
        self.assertNotIn("speaker", publish.lower())


class FailureReasonTests(unittest.TestCase):
    def test_rejected_publication_names_its_fixed_reason(self):
        """Speakers 0.26.96/Control 0.10.42: the release job printed only
        "Rejected", so nobody could tell which check stopped publication."""
        import subprocess
        import sys

        script = pathlib.Path(__file__).resolve().parents[1] / "scripts" / "publish_control_release.py"
        result = subprocess.run(
            [sys.executable, str(script)],
            env={"GITHUB_EVENT_NAME": "pull_request"},
            capture_output=True,
            text=True,
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertIn(
            "Control publication failed closed: Rejected: only this repository main push may publish",
            result.stderr,
        )


class MissedReleaseRecoveryTests(unittest.TestCase):
    """Control 0.10.42: the tag was created but the release was not, and the
    next main push with the same version only printed "version unchanged"."""

    @staticmethod
    def run_git(versions):
        def run(*args):
            if args[0] == "show":
                return '{"version":"%s"}' % versions[args[1].split(":")[0]]
            raise AssertionError(args)

        return run

    def test_tag_without_release_at_earlier_main_commit_gets_its_release(self):
        api = FakeAPI(target=OLD)
        result = publisher.recover(
            api, S, self.run_git({S: NAME, OLD: NAME}), lambda old, new: True
        )
        self.assertEqual(result, "published missed release")
        self.assertEqual(api.release["target_commitish"], OLD)
        self.assertEqual(api.target, OLD)
        self.assertFalse(any(c[:2] == ("POST", "git/refs") for c in api.calls))

    def test_unchanged_version_never_published_is_published_at_verified_commit(self):
        api = FakeAPI()
        self.assertEqual(
            publisher.recover(api, S, self.run_git({S: NAME}), lambda *a: False),
            "published",
        )
        self.assertEqual(api.target, S)
        self.assertEqual(api.release["target_commitish"], S)

    def test_unchanged_released_version_only_reads(self):
        release = {
            "tag_name": NAME,
            "target_commitish": OLD,
            "name": NAME,
            "draft": False,
            "prerelease": False,
        }
        api = FakeAPI(target=OLD, release=release)
        self.assertEqual(
            publisher.recover(
                api, S, self.run_git({S: NAME, OLD: NAME}), lambda *a: True
            ),
            "version unchanged",
        )
        self.assertTrue(all(c[0] == "GET" for c in api.calls))

    def test_foreign_or_mismatched_tag_is_never_released(self):
        for ancestor, tag_version in [(False, NAME), (True, "0.10.19")]:
            with self.subTest(ancestor=ancestor, tag_version=tag_version):
                api = FakeAPI(target=OLD)
                with self.assertRaises(publisher.Rejected):
                    publisher.recover(
                        api,
                        S,
                        self.run_git({S: NAME, OLD: tag_version}),
                        lambda *a, ancestor=ancestor: ancestor,
                    )
                self.assertTrue(all(c[0] == "GET" for c in api.calls))

    def test_lost_release_response_is_read_back_not_resent(self):
        api = FakeAPI()

        def lost(method, path, body=None):
            result = api(method, path, body)
            if (method, path) == ("POST", "releases"):
                raise publisher.Rejected("GitHub API unreachable for POST")
            return result

        self.assertEqual(publisher.publish(lost, NAME, S), "published")
        self.assertEqual(sum(c[:2] == ("POST", "releases") for c in api.calls), 1)

    def test_manual_main_run_only_recovers(self):
        env = {
            "GITHUB_EVENT_NAME": "workflow_dispatch",
            "GITHUB_REF": "refs/heads/main",
            "GITHUB_REPOSITORY": publisher.REPOSITORY,
            "GITHUB_SHA": S,
        }
        self.assertIsNone(publisher.candidate(env, lambda *args: S))
        with self.assertRaises(publisher.Rejected):
            publisher.candidate({**env, "GITHUB_REF": "refs/heads/x"}, lambda *a: S)
        with self.assertRaises(publisher.Rejected):
            publisher.candidate(env, lambda *args: OLD)


class TransientAPITests(unittest.TestCase):
    def api_with(self, responses):
        calls = []

        class Fixture(publisher.urllib.request.BaseHandler):
            handler_order = 100

            def https_open(self, request):
                calls.append(request.get_method())
                code = responses.pop(0)
                if code != 200:
                    raise publisher.urllib.error.HTTPError(
                        request.full_url, code, "fixture", Message(), BytesIO(b"")
                    )
                response = addinfourl(BytesIO(b"{}"), Message(), request.full_url, 200)
                response.msg = "OK"
                return response

        api = publisher.API("inert-fixture-token")
        api.opener.add_handler(Fixture())
        return api, calls

    def test_reads_retry_server_errors_and_writes_do_not(self):
        sleeps = []
        original = publisher.time.sleep
        publisher.time.sleep = sleeps.append
        try:
            api, calls = self.api_with([503, 502, 200])
            self.assertEqual(api("GET", "releases/tags/x"), (200, {}))
            self.assertEqual(calls, ["GET", "GET", "GET"])
            api, calls = self.api_with([503])
            with self.assertRaises(publisher.Rejected):
                api("POST", "releases", {})
            self.assertEqual(calls, ["POST"])
            api, calls = self.api_with([503] * 4)
            with self.assertRaises(publisher.Rejected):
                api("GET", "releases/tags/x")
            self.assertEqual(len(calls), 4)
        finally:
            publisher.time.sleep = original
