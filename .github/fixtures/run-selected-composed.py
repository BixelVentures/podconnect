import json
import os
import pathlib
import re
import subprocess
import sys
import time

mode = sys.argv[1]
prefix = "pc52-selected-"
root = pathlib.Path(os.environ.get("PC_FIXTURE_ROOT", "/work"))
ready = root / (prefix + mode + "-ready.json")
ready.unlink(missing_ok=True)
env = dict(os.environ, PC_NATIVE_START_READY_FILE=str(ready))
if env.get("PC_SHUTDOWN_JOURNAL"):
    for item in (root / "shutdown-songs.db", root / "shutdown-music.fifo"):
        item.unlink(missing_ok=True)
    env.update(PC70_COLD_DATABASE=str(root / "shutdown-songs.db"), PC70_COLD_FIFO=str(root / "shutdown-music.fifo"))
if mode == "actual-c-selected-successor":
    env["PC_NATIVE_START_EXPECT_TWO"] = "1"
with (
    (root / (prefix + mode + "-c.log")).open("w") as c_log,
    (root / (prefix + mode + "-native.log")).open("w") as n_log,
):
    c = subprocess.Popen(
        ["src/airplay_callback_owner", "--native-start-wire"],
        env=env,
        stdout=c_log,
        stderr=subprocess.STDOUT,
    )
    until = time.monotonic() + 5
    while not ready.exists() and c.poll() is None and time.monotonic() < until:
        time.sleep(0.01)
    if not ready.exists():
        c.terminate()
        c.wait()
        raise SystemExit("C ready missing")
    info = json.loads(ready.read_text())
    env.update(
        PC_NATIVE_START_URL=info["url"],
        PC_NATIVE_START_PIPE=info["pipe"],
        PC_NATIVE_START_MODE=mode,
    )
    n = subprocess.run(
        [
            os.environ.get("PC_NATIVE_TEST_BIN", "/work/native-tests"),
            "-test.v",
            "-test.timeout=20s",
            "-test.run",
            "^TestPreparedDealerSTARTUsesRealFIFOAndPreservesTransfer/" + mode + "$",
        ],
        env=env,
        stdout=n_log,
        stderr=subprocess.STDOUT,
        check=False,
    )
    try:
        c_status = c.wait(timeout=27)
    except subprocess.TimeoutExpired:
        c.kill()
        c_status = c.wait()
    if not n.returncode and not c_status and env.get("PC_SHUTDOWN_JOURNAL"):
        import shutil
        shutil.copyfile(env["PC70_COLD_DATABASE"], root / "shutdown-terminal.db")
        with (root / "shutdown-reload.log").open("w") as reload_log:
            reload_env = dict(env, PC70_COLD_RESTART="1", PC_JOURNAL_RELOAD_READ="1")
            reloaded = subprocess.run(["src/airplay_callback_owner", "--native-start-wire"], env=reload_env, stdout=reload_log, stderr=subprocess.STDOUT, check=False)
        if reloaded.returncode:
            raise SystemExit("C journal reload failed; see shutdown-reload.log")
        unknown_db = root / "shutdown-unknown.db"
        shutil.copyfile(root / "shutdown-terminal.db", unknown_db)
        with (root / "shutdown-unknown.log").open("w") as unknown_log:
            unknown_env = dict(reload_env, PC70_COLD_DATABASE=str(unknown_db), PC_JOURNAL_INJECT_UNKNOWN="1")
            unknown = subprocess.run(["src/airplay_callback_owner", "--native-start-wire"], env=unknown_env, stdout=unknown_log, stderr=subprocess.STDOUT, check=False)
        if unknown.returncode:
            raise SystemExit("C UNKNOWN journal reload failed; see shutdown-unknown.log")
    skew_ms = None
    if not n.returncode and not c_status and os.getenv("PC_REQUIRE_FIRST_SYNC_CLOCK"):
        match = re.search(
            r"Start sync packet sent.*cur_ts=(\d+\.\d+), clock=(\d+\.\d+)",
            (root / (prefix + mode + "-c.log")).read_text(),
        )
        if match:
            skew_ms = abs(float(match.group(2)) - float(match.group(1))) * 1000
    print(
        json.dumps(
            {
                "mode": mode,
                "native_exit": n.returncode,
                "c_exit": c_status,
                "first_sync_skew_ms": skew_ms,
            }
        )
    )
    raise SystemExit(
        n.returncode
        or c_status
        or (
            os.getenv("PC_REQUIRE_FIRST_SYNC_CLOCK")
            and (skew_ms is None or skew_ms > 500)
        )
    )

