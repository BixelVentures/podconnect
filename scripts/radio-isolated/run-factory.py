#!/usr/bin/env python3
"""Explicit offline fixture runner. It touches only its named containers/cgroup parent.

No images are pulled, no host network/PID/cgroup namespace is shared, no kernel
administrative capability is added, and only the new task-owned cgroup parent is writable.
Service uid setup uses the minimal ordinary capabilities listed below.
"""
import argparse, datetime, hashlib, json, pathlib, shutil, subprocess, uuid


def command(args, *, check=True):
    result = subprocess.run(["docker", *args], text=True, capture_output=True)
    if check and result.returncode:
        raise RuntimeError(result.stderr.strip())
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-root", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--scenario", choices=("start-stop", "restart", "resume", "controls", "cycles", "factory-restart", "move-roundtrip", "move-coexist", "selected", "discovery", "evolution"), default="start-stop")
    parser.add_argument("--reject-delegation", action="store_true", help="expect the wrapper to reject a mismatched scope before entrypoint")
    parser.add_argument("--image", default="ghcr.io/bixelventures/aarch64-podconnect:0.26.7")
    args = parser.parse_args()
    source = args.source_root.resolve(strict=True)
    output = args.output.resolve()
    if output.exists():
        raise SystemExit("output must be a fresh directory")
    output.mkdir(mode=0o700)
    scripts = pathlib.Path(__file__).resolve().parent
    policy = scripts.parents[1] / "podconnect/manager/testdata/radio-seccomp"
    artifacts = source / "wire-client-artifacts"
    for name in ("native-radio-final-http.test", "go-radio-full-chain.test"):
        shutil.copy2(artifacts / name, output / name)
    shutil.copy2(source / "owntone/src/airplay_callback_owner", output / "qualified-c-fixture")
    shutil.copy2(source / "owntone/src/owntone", output / "production-c-cli")
    identity = "pv-radio-factory-" + uuid.uuid4().hex[:12]
    anchor, fixture = identity + "-anchor", identity + "-fixture"
    parent = "/" + identity
    artifacts_hashes = {name: hashlib.sha256((output / name).read_bytes()).hexdigest() for name in ("native-radio-final-http.test", "go-radio-full-chain.test")}
    artifacts_hashes.update({"qualified_c_fixture": hashlib.sha256((output / "qualified-c-fixture").read_bytes()).hexdigest(), "production_c_cli": hashlib.sha256((output / "production-c-cli").read_bytes()).hexdigest(), "delegation_wrapper": hashlib.sha256((scripts / "delegate-entry.sh").read_bytes()).hexdigest(), "seccomp": hashlib.sha256((policy / "radio-clone3-seccomp.json").read_bytes()).hexdigest()})
    receipt = {"artifacts_sha256": artifacts_hashes, "status": "NOT_RUN", "parent": parent, "anchor": anchor,
               "fixture": fixture, "image": args.image, "scenario": args.scenario, "household": False,
               "negative_delegation": args.reject_delegation, "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat()}
    try:
        command(["run", "-d", "--name", anchor, "--label", "podview.radio.fixture=" + identity, "--pull", "never", "--network", "none",
                 "--cgroup-parent", parent, "--cap-drop", "ALL", "--read-only",
                 "--cpus", "0.1", "--memory", "32m", "--pids-limit", "8",
                 "alpine:latest", "sleep", "300"])
        run = ["run", "--name", fixture, "--label", "podview.radio.fixture=" + identity, "--pull", "never", "--network", "none",
               "--cgroupns", "private", "--cgroup-parent", parent,
               "--cap-drop", "ALL", "--security-opt", "seccomp=" + str(policy / "radio-clone3-seccomp.json"),
               "--read-only", "--cpus", "2", "--memory", "2g", "--pids-limit", "256",
               "--tmpfs", "/tmp:rw,exec,nosuid,nodev,size=536870912,mode=1777",
               "--tmpfs", "/run:rw,nosuid,nodev,size=33554432,mode=755"]
        expected_scope = parent if not args.reject_delegation else "/pv-radio-wrong-" + uuid.uuid4().hex[:12]
        run += ["--env", "PODCONNECT_RADIO_DELEGATION_SCOPE=" + expected_scope]
        for capability in ("CHOWN", "DAC_OVERRIDE", "FOWNER", "SETUID", "SETGID", "KILL"):
            run += ["--cap-add", capability]
        for src, dst, readonly in [("/sys/fs/cgroup" + parent, "/run/task-cgroup" + parent, False),
                                    (str(source / "owntone"), "/work", True),
                                    (str(output), "/out", False),
                                    (str(scripts), "/test-control", True),
                                    (str(scripts / "owntone-test-entry.sh"), "/usr/sbin/owntone", True)]:
            run += ["--mount", f"type=bind,src={src},dst={dst}" + (",readonly" if readonly else "")]
        if args.scenario in ("restart", "resume", "controls", "cycles", "factory-restart"):
            run += ["--env", "PC_RADIO_CHAIN_RESTART=1"]
        if args.scenario in ("resume", "controls", "cycles", "factory-restart"):
            run += ["--env", "PC_RADIO_CHAIN_RESUME=1", "--env", "PC_NATIVE_RADIO_HTTP_EXPECT_RETURN=1"]
        if args.scenario in ("controls", "cycles", "factory-restart"):
            run += ["--env", "PC_RADIO_CHAIN_CONTROLS=1"]
        if args.scenario in ("cycles", "factory-restart"):
            run += ["--env", "PC_RADIO_CHAIN_CYCLES=1"]
        if args.scenario == "factory-restart":
            run += ["--env", "PC_RADIO_CHAIN_FACTORY_RESTART=1"]
        if args.scenario.startswith("move-") or args.scenario in ("discovery","evolution"):
            run += ["--env", "PC_RADIO_HTTP_SECOND_TARGET_ID=2", "--env", "PC_NATIVE_RADIO_HTTP_SECOND_TARGET_ID=2", "--env", "PC_RADIO_CHAIN_MOVE=" + (args.scenario.removeprefix("move-") if args.scenario.startswith("move-") else "")]
        if args.scenario == "move-coexist":
            run += ["--env", "PC_NATIVE_RADIO_HTTP_EXPECT_RETURN=1"]
        if args.scenario=="evolution":
            run += ["--env", "PC_RADIO_CHAIN_EVOLUTION=1", "--env", "PC_NATIVE_RADIO_HTTP_BOOT_EMPTY=1", "--env", "PC_NATIVE_RADIO_HTTP_SELECTED_PROVIDER=1", "--env", "PC_NATIVE_RADIO_HTTP_EXPECT_RETURN=selected"]
        if args.scenario=="discovery":
            run += ["--env", "PC_RADIO_CHAIN_DISCOVERY=1"]
        if args.scenario == "selected":
            run += ["--env", "PC_RADIO_CHAIN_SELECTED=1", "--env", "PC_NATIVE_RADIO_HTTP_SELECTED_PROVIDER=1", "--env", "PC_NATIVE_RADIO_HTTP_EXPECT_RETURN=selected"]
        run += ["--entrypoint", "/bin/sh", args.image, "/test-control/delegate-entry.sh", "/bin/sh", "/test-control/guest-factory.sh"]
        result = command(run, check=False)
        (output / "container.log").write_text(result.stdout + result.stderr)
        executed = (output / "delegation-executed").exists()
        if args.reject_delegation:
            accepted = result.returncode == 125 and not executed and "exact delegated mount required" in result.stderr
        else:
            accepted = result.returncode == 0 and executed
        receipt.update(status="PASS" if accepted else "FAIL", exit_code=result.returncode, original_entrypoint_executed=executed)
        inspect = command(["inspect", fixture], check=False)
        if inspect.returncode == 0:
            value = json.loads(inspect.stdout)[0]
            receipt["actual_container"] = {"image": value["Image"], "state": value["State"],
                "cgroup_parent": value["HostConfig"]["CgroupParent"],
                "cgroupns_mode": value["HostConfig"].get("CgroupnsMode"),
                "readonly_rootfs": value["HostConfig"].get("ReadonlyRootfs"),
                "delegation_mounts": [{"source": m["Source"], "destination": m["Destination"], "rw": m["RW"]} for m in value["Mounts"] if m["Destination"].startswith("/run/task-cgroup")],
                "privileged": value["HostConfig"]["Privileged"],
                "network_mode": value["HostConfig"]["NetworkMode"],
                "pid_mode": value["HostConfig"]["PidMode"],
                "cap_add": value["HostConfig"]["CapAdd"], "cap_drop": value["HostConfig"]["CapDrop"]}
    finally:
        for name in (fixture, anchor):
            ownership = command(["inspect", name], check=False)
            if ownership.returncode != 0:
                receipt.setdefault("cleanup", {})[name] = True
                continue
            owned = json.loads(ownership.stdout)[0]["Config"].get("Labels", {}).get("podview.radio.fixture") == identity
            if not owned:
                receipt.setdefault("cleanup", {})[name] = False
                continue
            cleaned = command(["rm", "-f", name], check=False)
            receipt.setdefault("cleanup", {})[name] = cleaned.returncode == 0
        (output / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(json.dumps(receipt, indent=2))
    return 0 if receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
