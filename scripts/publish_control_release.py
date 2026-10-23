"""Publish only a verified main version change; never replace releases or tags."""

import json
import os
import re
import subprocess
import urllib.error
import urllib.request

SHA = re.compile(r"[0-9a-f]{40}\Z")
IDENT = r"(?:0|[1-9][0-9]*)"
PRE = rf"(?:{IDENT}|[0-9]*[A-Za-z-][0-9A-Za-z-]*)"
VERSION = re.compile(
    rf"({IDENT})\.({IDENT})\.({IDENT})(?:-({PRE}(?:\.{PRE})*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?\Z"
)
MANIFEST = "custom_components/podconnect/manifest.json"
REPOSITORY = "BixelVentures/podconnect"


class Rejected(RuntimeError):
    pass


def version(value):
    match = VERSION.fullmatch(value) if isinstance(value, str) else None
    if not match or not any(int(match[i]) for i in (1, 2, 3)):
        raise Rejected("invalid positive SemVer")
    return value


def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()


def candidate(env, run=git):
    if (
        env.get("GITHUB_EVENT_NAME"),
        env.get("GITHUB_REF"),
        env.get("GITHUB_REPOSITORY"),
    ) != ("push", "refs/heads/main", REPOSITORY):
        raise Rejected("only this repository main push may publish")
    sha, before = env.get("GITHUB_SHA", ""), env.get("RELEASE_BEFORE", "")
    if not SHA.fullmatch(sha) or not SHA.fullmatch(before) or set(before) == {"0"}:
        raise Rejected("invalid commit boundary")
    if run("rev-parse", "HEAD") != sha:
        raise Rejected("checkout is not the verified commit")
    run("merge-base", "--is-ancestor", before, sha)
    old = version(json.loads(run("show", f"{before}:{MANIFEST}"))["version"])
    new = version(json.loads(run("show", f"{sha}:{MANIFEST}"))["version"])
    return None if old == new else (new, sha)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, newurl):
        raise Rejected("GitHub API redirect refused")


class API:
    def __init__(self, token):
        if not token:
            raise Rejected("missing action token")
        self.token = token
        self.opener = urllib.request.build_opener(NoRedirect())

    def __call__(self, method, path, body=None):
        request = urllib.request.Request(
            f"https://api.github.com/repos/{REPOSITORY}/{path}",
            data=None if body is None else json.dumps(body).encode(),
            method=method,
            headers={
                "Authorization": f"Bearer {self.token}",
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": "2022-11-28",
                "Content-Type": "application/json",
            },
        )
        try:
            with self.opener.open(request, timeout=30) as response:
                return response.status, json.load(response)
        except urllib.error.HTTPError as error:
            if error.code == 404:
                return 404, None
            raise Rejected(
                f"GitHub API rejected {method}: status {error.code}"
            ) from None


def tag_target(api, name):
    status, ref = api("GET", f"git/ref/tags/{name}")
    if status == 404:
        return None
    if (
        status != 200
        or not isinstance(ref, dict)
        or ref.get("ref") != f"refs/tags/{name}"
    ):
        raise Rejected("invalid tag reference")
    obj = ref.get("object")
    for _ in range(5):
        if not isinstance(obj, dict) or not SHA.fullmatch(obj.get("sha", "")):
            raise Rejected("invalid tag object")
        if obj.get("type") == "commit":
            return obj["sha"]
        if obj.get("type") != "tag":
            raise Rejected("tag does not resolve to a commit")
        status, tag = api("GET", f"git/tags/{obj['sha']}")
        if status != 200 or not isinstance(tag, dict) or tag.get("sha") != obj["sha"]:
            raise Rejected("invalid annotated tag")
        obj = tag.get("object")
    raise Rejected("tag nesting limit")


def release_valid(release, name, sha):
    if (
        not isinstance(release, dict)
        or release.get("tag_name") != name
        or release.get("target_commitish") != sha
        or release.get("name") != name
        or release.get("draft") is not False
        or release.get("prerelease") is not bool(VERSION.fullmatch(name)[4])
    ):
        raise Rejected("existing release metadata differs from verified candidate")


def current_main(api, sha):
    code, ref = api("GET", "git/ref/heads/main")
    obj = ref.get("object") if isinstance(ref, dict) else None
    if (
        code != 200
        or not isinstance(ref, dict)
        or ref.get("ref") != "refs/heads/main"
        or not isinstance(obj, dict)
        or obj.get("type") != "commit"
        or obj.get("sha") != sha
    ):
        raise Rejected("main advanced or cannot be verified")


def publish(api, name, sha):
    version(name)
    if not SHA.fullmatch(sha):
        raise Rejected("invalid publication commit")
    status, release = api("GET", f"releases/tags/{name}")
    if status not in (200, 404):
        raise Rejected("unknown release state")
    target = tag_target(api, name)
    if target is not None and target != sha:
        raise Rejected("tag collision: refusing retag")
    if status == 200:
        release_valid(release, name, sha)
        if target != sha:
            raise Rejected("release tag missing")
        return "already published"
    # Reserve the exact literal tag first. The releases API must never select
    # an existing foreign tag or silently resolve a mutable branch name.
    current_main(api, sha)
    if target is None:
        code, ref = api("POST", "git/refs", {"ref": f"refs/tags/{name}", "sha": sha})
        if code != 201 or not isinstance(ref, dict):
            raise Rejected("tag creation failed")
    if tag_target(api, name) != sha:
        raise Rejected("tag changed before release creation")
    current_main(api, sha)
    code, release = api(
        "POST",
        "releases",
        {
            "tag_name": name,
            "target_commitish": sha,
            "name": name,
            "draft": False,
            "prerelease": bool(VERSION.fullmatch(name)[4]),
            "generate_release_notes": True,
        },
    )
    if code != 201:
        raise Rejected("release creation failed")
    release_valid(release, name, sha)
    if tag_target(api, name) != sha:
        raise Rejected("tag changed during release creation")
    return "published"


if __name__ == "__main__":
    try:
        selected = candidate(os.environ)
        print(
            "version unchanged"
            if selected is None
            else publish(API(os.environ.get("GITHUB_TOKEN")), *selected)
        )
    except (
        Rejected,
        KeyError,
        ValueError,
        TypeError,
        OSError,
        subprocess.CalledProcessError,
    ) as error:
        raise SystemExit(
            f"Control publication failed closed: {type(error).__name__}"
        ) from None
