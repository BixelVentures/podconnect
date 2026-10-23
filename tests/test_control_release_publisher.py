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
