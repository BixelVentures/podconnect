"""Actual worker composition: ordinary S6, default caps/seccomp, network none.
No cgroup delegation/custom namespace/full_access/host PID/household access.
"""

import argparse
import datetime
import hashlib
import json
import pathlib
import shutil
import subprocess
import uuid

ROOT = pathlib.Path("/private/tmp/radio-v2x-runtime-20261103")
NATIVE = pathlib.Path("/private/tmp/radio64-native-compose-20261103")


def docker(args, check=True):
    p = subprocess.run(["docker", *args], text=True, capture_output=True, check=False)
    if check and p.returncode:
        raise RuntimeError(p.stdout + p.stderr)
    return p


def main():
    global ROOT, NATIVE
    a = argparse.ArgumentParser()
    a.add_argument(
        "--scenario",
        required=True,
        choices=(
            "start-stop",
            "restart",
            "resume",
            "controls",
            "cycles",
            "factory-restart",
            "move-roundtrip",
            "move-coexist",
            "selected",
            "discovery",
            "evolution",
            "unknown",
            "unavailable",
        ),
    )
    a.add_argument("--runtime-root", type=pathlib.Path, default=ROOT)
    a.add_argument("--composition-root", type=pathlib.Path, default=NATIVE)
    a.add_argument("--image", default="radio-v2x-worker-runtime:local")
    args = a.parse_args()
    ROOT = args.runtime_root.resolve()
    NATIVE = args.composition_root.resolve()
    out = ROOT / ("result-" + args.scenario + "-" + uuid.uuid4().hex[:6])
    out.mkdir(mode=0o755)
    for src, dst in [
        (ROOT / "bin/go-radio-full-chain.test", "go-radio-full-chain.test"),
        (NATIVE / "bin/native-fixture64", "native-radio-final-http.test"),
        (NATIVE / "bin/owntone-fixture", "qualified-c-fixture"),
        (NATIVE / "bin/owntone", "production-c-cli"),
    ]:
        shutil.copy2(src, out / dst)
    (out / "fixture.env").write_text("")
    (out / "fixture.env").chmod(0o644)
    empty = ROOT / "empty-services"
    empty.mkdir(exist_ok=True)
    name = "pv-radio-worker-" + uuid.uuid4().hex[:12]
    cmd = [
        "run",
        "--name",
        name,
        "--label",
        "podview.radio.worker.fixture=" + name,
        "--pull",
        "never",
        "--network",
        "none",
        "--tmpfs",
        "/tmp:rw,exec,nosuid,nodev,size=536870912,mode=1777",
        "--tmpfs",
        "/run:rw,exec,nosuid,nodev,size=33554432,mode=755",
        "--tmpfs",
        "/data:rw,nosuid,nodev,size=33554432,mode=755",
        "--tmpfs",
        "/config:rw,nosuid,nodev,size=33554432,mode=755",
    ]
    mounts = [
        (NATIVE / "owntone", "/c-source", True),
        (NATIVE / "owntone", "/work", True),
        (out, "/out", False),
        (pathlib.Path(__file__).resolve().parent, "/test-control", True),
        (out / "fixture.env", "/test-control/fixture.env", True),
        (empty, "/etc/services.d", True),
        (empty, "/etc/s6-overlay/s6-rc.d/user/contents.d", True),
    ]
    for src, dst, ro in mounts:
        cmd += [
            "--mount",
            f"type=bind,src={src},dst={dst}" + (",readonly" if ro else ""),
        ]
    env = {}
    if args.scenario in ("restart", "resume", "controls", "cycles", "factory-restart"):
        env["PC_RADIO_CHAIN_RESTART"] = "1"
    if args.scenario in ("resume", "controls", "cycles", "factory-restart"):
        env.update(PC_RADIO_CHAIN_RESUME="1", PC_NATIVE_RADIO_HTTP_EXPECT_RETURN="1")
    if args.scenario in ("controls", "cycles", "factory-restart"):
        env["PC_RADIO_CHAIN_CONTROLS"] = "1"
    if args.scenario in ("cycles", "factory-restart"):
        env["PC_RADIO_CHAIN_CYCLES"] = "1"
    if args.scenario == "factory-restart":
        env["PC_RADIO_CHAIN_FACTORY_RESTART"] = "1"
    if args.scenario.startswith("move-") or args.scenario in ("discovery", "evolution"):
        env.update(
            PC_RADIO_HTTP_SECOND_TARGET_ID="2",
            PC_NATIVE_RADIO_HTTP_SECOND_TARGET_ID="2",
            PC_RADIO_CHAIN_MOVE=args.scenario.removeprefix("move-")
            if args.scenario.startswith("move-")
            else "",
        )
    if args.scenario == "move-coexist":
        env["PC_NATIVE_RADIO_HTTP_EXPECT_RETURN"] = "1"
    if args.scenario == "evolution":
        env.update(
            PC_RADIO_CHAIN_EVOLUTION="1",
            PC_NATIVE_RADIO_HTTP_BOOT_EMPTY="1",
            PC_NATIVE_RADIO_HTTP_SELECTED_PROVIDER="1",
            PC_NATIVE_RADIO_HTTP_EXPECT_RETURN="selected",
        )
    if args.scenario == "discovery":
        env["PC_RADIO_CHAIN_DISCOVERY"] = "1"
    if args.scenario == "unknown":
        env["PC_RADIO_CHAIN_UNKNOWN"] = "1"
    if args.scenario == "unavailable":
        env["PC_RADIO_CHAIN_UNAVAILABLE"] = "1"
        env["PC_NATIVE_RADIO_HTTP_EXPECT_RETURN"] = "unchanged"
        cmd += [
            "--mount",
            f"type=bind,src={ROOT}/bin/worker-unavailable,dst=/usr/libexec/podconnect-radio-worker,readonly",
        ]
    if args.scenario == "selected":
        env.update(
            PC_RADIO_CHAIN_SELECTED="1",
            PC_NATIVE_RADIO_HTTP_SELECTED_PROVIDER="1",
            PC_NATIVE_RADIO_HTTP_EXPECT_RETURN="selected",
        )
    for k, v in env.items():
        cmd += ["--env", k + "=" + v]
    cmd += [
        "--entrypoint",
        "/init",
        args.image,
        "/command/with-contenv",
        "/bin/sh",
        "/test-control/guest-worker.sh",
    ]
    receipt = {
        "scenario": args.scenario,
        "started_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "status": "NOT_RUN",
        "household": False,
        "profile": "ordinary S6/default Docker capabilities/default seccomp/network none; no delegated cgroup",
        "adapter": "compiled same-PID test exec adapter; actual production export/initialize; finite provider/inert receiver C fixture runtime",
        "artifacts_sha256": {
            p.name: hashlib.sha256(p.read_bytes()).hexdigest()
            for p in out.iterdir()
            if p.is_file()
        },
        "command": cmd,
        "manager_source_pins": json.loads(
            (ROOT / "manager-source-pins.json").read_text()
        ),
        "helper_sha256": hashlib.sha256(
            (ROOT / "bin/podconnect-radio-worker").read_bytes()
        ).hexdigest(),
        "harness_sha256": {
            p.name: hashlib.sha256(p.read_bytes()).hexdigest()
            for p in pathlib.Path(__file__).resolve().parent.iterdir()
            if p.is_file()
        },
    }
    try:
        p = docker(cmd, False)
        (out / "container.log").write_text(p.stdout + p.stderr)
        ins = docker(["inspect", name])
        i = json.loads(ins.stdout)[0]
        receipt["container"] = i
        h = i["HostConfig"]
        profile_ok = (
            not h["Privileged"]
            and h.get("CgroupParent", "") == ""
            and not h.get("CapAdd")
            and not h.get("CapDrop")
            and not h.get("SecurityOpt")
            and h.get("PidMode", "") == ""
            and h.get("NetworkMode") == "none"
            and not any("cgroup" in m["Destination"] for m in i["Mounts"])
            and i["Path"] == "/init"
        )
        receipt["profile_verified"] = profile_ok
        logs = "".join(p.read_text(errors="replace") for p in out.glob("*.log"))
        markers = {
            "factory-restart": [
                "manual41",
                "two actual Radio/Spotify cycles",
                "new configured factory/same private DB",
            ],
            "unknown": ["actual sender SIGKILL"],
            "unavailable": ["helper-unavailable bootstrap"],
            "move-coexist": ["while radio B continues fresh RTP"],
            "move-roundtrip": [
                "actual manager Move r1→r0",
                "actual manager Move r0→r1",
            ],
            "selected": ["authenticated radio preempt/drain"],
            "discovery": ["discovery-unavailable"],
            "evolution": ["boot-empty global absence", "evolved-absence A selected35"],
        }
        scenario_markers = markers.get(args.scenario, [])
        receipt["scenario_markers_required"] = scenario_markers
        receipt["scenario_markers_observed"] = {m: m in logs for m in scenario_markers}
        passed = (
            profile_ok
            and all(m in logs for m in scenario_markers)
            and p.returncode == 0
            and (out / "worker-entry-executed").exists()
            and (out / "factory-chain-result.json").exists()
            and "--- PASS: TestRadioProductionFactoryExternalChain" in logs
            and "--- SKIP:" not in logs
        )
        receipt.update(status="PASS" if passed else "FAIL", exit_code=p.returncode)
    finally:
        receipt["cleanup"] = docker(["rm", "-f", name], False).returncode == 0
        (out / "receipt.json").write_text(json.dumps(receipt, indent=2) + "\n")
    print(out, receipt["status"])
    return 0 if receipt["status"] == "PASS" else 1


if __name__ == "__main__":
    raise SystemExit(main())
