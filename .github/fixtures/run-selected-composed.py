import json
import os
import pathlib
import re
import socket
import subprocess
import sys
import time

def run_primary_radio_http():
    import pwd
    import tempfile

    root = pathlib.Path(os.environ.get("PC_FIXTURE_ROOT", "/work"))
    token = "2" * 64  # Fixed inert control token confined to this fixture.
    with tempfile.TemporaryDirectory(prefix="primary-radio-http-", dir=root) as temp:
        private = pathlib.Path(temp)
        ready = private / "ready"
        done = private / "done"
        config = private / "owntone.conf"
        # httpd_init uses the configured port in its actual worker pool.
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        uid = pwd.getpwuid(os.geteuid()).pw_name
        config.write_text(
            f'general {{ uid = "{uid}" db_path = "{private}/owntone.db" '
            f'logfile = "{private}/owntone.log" bind_address = "127.0.0.1" '
            f'ipv6 = false admin_password = "inert-http-admin" '
            f'radio_handoff_control_token = "{token}" }}\n'
            f'library {{ name = "Private primary owner" port = {port} '
            f'pipe_autostart = false directories = {{ "{private}" }} }}\n'
        )
        env = dict(
            os.environ,
            PC_RADIO_HTTP_CONFIG=str(config),
            PC_PRIMARY_HTTP_READY=str(ready),
            PC_PRIMARY_HTTP_DONE=str(done),
        )
        env.pop("PODCONNECT_RADIO_SENDER", None)
        with (private / "c.log").open("w") as c_log:
            c = subprocess.Popen(
                ["src/airplay_callback_owner", "--radio-primary-http-owner"],
                cwd=root, env=env, stdout=c_log, stderr=subprocess.STDOUT,
            )
            try:
                deadline = time.monotonic() + 5
                while c.poll() is None and time.monotonic() < deadline:
                    if ready.exists():
                        try:
                            with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                                break
                        except OSError:
                            pass
                    time.sleep(0.025)
                else:
                    raise RuntimeError("production C HTTP listener did not become ready")
                endpoint = f"http://127.0.0.1:{port}"
                if ready.read_text().strip() != endpoint:
                    raise RuntimeError("unexpected C HTTP listener address")
                native = subprocess.run(
                    [
                        os.environ.get("PC_NATIVE_TEST_BIN", "/work/native-tests"),
                        "-test.v", "-test.timeout=12s", "-test.run", "^TestPrimaryRadioActorOwnToneHTTP$",
                    ],
                    cwd=root, env=dict(env, PC_PRIMARY_HTTP_URL=endpoint, PC_PRIMARY_HTTP_TOKEN=token),
                    timeout=15, check=False,
                )
                if native.returncode:
                    raise RuntimeError("Native actor failed production C HTTP exercise")
                done.touch()
                if c.wait(timeout=5):
                    raise RuntimeError("C did not verify RTP and one exact teardown")
                print((private / "c.log").read_text())
            except (RuntimeError, subprocess.TimeoutExpired):
                print((private / "c.log").read_text(), file=sys.stderr)
                raise
            finally:
                if c.poll() is None:
                    c.kill()
                    c.wait()


def run_primary_cold_detachment():
    import contextlib
    import fcntl
    import tempfile

    # /data must remain private to the test because the production proof is
    # intentionally pinned to /data/media/spotify and its real lifetime lock.
    outer_ns = os.getenv("PC_FIXTURE_HOST_MOUNT_NS")
    if not outer_ns or outer_ns == os.readlink("/proc/self/ns/mnt"):
        raise SystemExit("cold detachment requires a private mount namespace")
    root = pathlib.Path(os.environ.get("PC_FIXTURE_ROOT", "/work"))
    logs = pathlib.Path(tempfile.mkdtemp(prefix="pc52-selected-primary-cold-", dir=root))
    data = pathlib.Path("/data")
    created_mountpoint = not data.exists()
    if data.is_symlink() or (not created_mountpoint and not data.is_dir()):
        raise SystemExit("invalid cold fixture mountpoint")
    if created_mountpoint:
        data.mkdir(mode=0o755)
    c_binary = ["src/airplay_callback_owner", "--native-start-wire"]
    native_binary = [
        os.environ.get("PC_NATIVE_TEST_BIN", "/work/native-tests"), "-test.v", "-test.timeout=15s",
        "-test.run", "^TestPreparedDealerSTARTUsesRealFIFOAndPreservesTransfer/actual-c-primary-same-target$",
    ]

    @contextlib.contextmanager
    def owner_lifetime(generation):
        fd = os.open(data / ".podconnect-primary-owner", os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            if fd != 3:  # Exact descriptor number used by both production owners.
                raise RuntimeError("cold fixture lifetime descriptor is not fd 3")
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
            yield dict(env, PODCONNECT_PRIMARY_OWNER_FENCE=generation * 32)
        finally:
            os.close(fd)

    def stop(process):
        if process.poll() is None:
            process.terminate()
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()

    def wait_ready(c, ready, port, fifo):
        deadline = time.monotonic() + 5
        endpoint = f"http://127.0.0.1:{port}"
        while c.poll() is None and time.monotonic() < deadline:
            if ready.exists():
                try:
                    info = json.loads(ready.read_text())
                    with socket.create_connection(("127.0.0.1", port), timeout=0.1):
                        break
                except (OSError, ValueError):
                    pass
            time.sleep(0.025)
        else:
            raise RuntimeError("C owner did not expose its fixture endpoints")
        if info.get("primary_url") != endpoint or info.get("pipe") != fifo:
            raise RuntimeError("unexpected cold owner fixture endpoints")
        return dict(PC_NATIVE_START_URL=info["url"], PC_NATIVE_START_PIPE=fifo,
                    PC_NATIVE_PRIMARY_HTTP_URL=endpoint)

    def run_live(name, generation, phase, marker, finish_native):
        ready = logs / f"{name}-ready.json"
        c_path = logs / f"{name}-c.log"
        with owner_lifetime(generation) as scoped, c_path.open("w") as c_log:
            scoped.update(phase, PC_NATIVE_START_READY_FILE=str(ready))
            c = subprocess.Popen(c_binary, cwd=root, env=scoped, pass_fds=(3,),
                                 stdout=c_log, stderr=subprocess.STDOUT)
            try:
                native_env = dict(scoped, **wait_ready(c, ready, port, scoped["PC70_COLD_FIFO"]))
                with (logs / f"{name}-native.log").open("w") as native_log:
                    native = subprocess.Popen(native_binary, cwd=root, env=native_env, pass_fds=(3,),
                                              stdout=native_log, stderr=subprocess.STDOUT)
                    try:
                        if c.wait(timeout=22):
                            raise RuntimeError(f"{name} C owner failed")
                        if finish_native and native.wait(timeout=4):
                            raise RuntimeError(f"{name} Native owner failed")
                        if name == "prior":
                            competing = os.open(data / ".podconnect-primary-owner", os.O_RDWR | os.O_NOFOLLOW)
                            try:
                                try:
                                    fcntl.flock(competing, fcntl.LOCK_EX | fcntl.LOCK_NB)
                                except BlockingIOError:
                                    print("PASS competing descriptor cannot acquire the old shared lifetime lock")
                                else:
                                    raise RuntimeError("old lifetime allowed a competing lock")
                            finally:
                                os.close(competing)
                    finally:
                        stop(native)
            finally:
                stop(c)
        print_marker(c_path, marker)

    def print_marker(path, prefix):
        marker = next((line for line in path.read_text().splitlines() if line.startswith(prefix)), None)
        if marker is None:
            raise RuntimeError(f"{path.name} lacks expected cold detachment proof")
        print(marker)

    def run_cold_read(name, generation, marker, **phase):
        c_path = logs / f"{name}-c.log"
        with owner_lifetime(generation) as scoped, c_path.open("w") as c_log:
            scoped.update(phase, PC70_COLD_RESTART="1")
            subprocess.run(c_binary, cwd=root, env=scoped, pass_fds=(3,),
                           stdout=c_log, stderr=subprocess.STDOUT, timeout=10, check=True)
        print_marker(c_path, marker)

    try:
        with tempfile.TemporaryDirectory(prefix="primary-cold-data-", dir=root) as source:
            pathlib.Path(source).chmod(0o755)
            subprocess.run(["mount", "--bind", source, str(data)], check=True)
            try:
                (data / "media").mkdir(mode=0o755)
                with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
                    sock.bind(("127.0.0.1", 0))
                    port = sock.getsockname()[1]
                env = dict(
                    os.environ,
                    PC70_COLD_DATABASE=str(data / "songs3.db"),
                    PC70_COLD_FIFO=str(data / "media/spotify"),
                    PC_NATIVE_COLD_RADIO_JOURNAL=str(data / "native-radio.json"),
                    PC_NATIVE_PRIMARY_COLD_PARK="1",
                    PC_NATIVE_PRIMARY_NEW_SELECTION="1",
                    PC_NATIVE_PRIMARY_HTTP_PORT=str(port),
                    PC_PRIMARY_HTTP_TOKEN="2" * 64,
                    PC_REQUIRE_RTP_AUDIO="1",
                    PC_NATIVE_START_MODE="actual-c-primary-same-target",
                )
                manager = bool(env.get("PC_PRIMARY_MANAGER_TEST_BIN"))
                if manager:
                    (data / "manager").mkdir(mode=0o700)
                    env["PC_PRIMARY_MANAGER_COLD_DATA"] = str(data / "manager")
                prior = dict(PC_NATIVE_PRIMARY_CONTROL_LOSS=str(logs / "disconnect"))
                if manager:
                    prior["PC_PRIMARY_MANAGER_COLD_PRIOR"] = "1"
                else:
                    prior["PC_NATIVE_PRIMARY_STOP_RESTART"] = "1"
                run_live("prior", "a", prior, "PASS real old START and encrypted P1 parked", manager)
                run_cold_read("witness", "b", "PASS restarted C recorded")
                current = dict(PC70_COLD_RESTART="1", PC_NATIVE_PRIMARY_COLD_REPLAY="1",
                               PC_NATIVE_PRIMARY_COLD_COMPOSED="1", PC_NATIVE_PRIMARY_COLD_FRESH_SPOTIFY="1")
                if env.get("PC_NATIVE_PRIMARY_COLD_GRACEFUL_SPOTIFY"):
                    if manager:
                        raise RuntimeError("graceful Spotify regression does not select a new station")
                    run_live("current", "c", current, "PASS fresh Spotify RTP then real Native Close", True)
                    run_cold_read("after-graceful", "d", "PASS archived P1 revalidated",
                                  PC_NATIVE_PRIMARY_COLD_AFTER_GRACEFUL="1")
                else:
                    current["PC_NATIVE_PRIMARY_COLD_FRESH_RADIO"] = "1"
                    run_live("current", "c", current, "PASS cold new owner authenticated fresh Spotify", True)
                    run_cold_read("after-p6", "d", "PASS restarted C recorded", PC_NATIVE_PRIMARY_COLD_NEXT_PARK="1")
            finally:
                subprocess.run(["umount", str(data)], check=True)
    finally:
        if created_mountpoint:
            data.rmdir()
        print(f"Cold detachment fixture logs: {logs}")


if sys.argv[1] == "actual-c-primary-cold-detachment":
    run_primary_cold_detachment()
    sys.exit(0)


if sys.argv[1] == "primary-radio-http":
    run_primary_radio_http()
    sys.exit(0)


mode = sys.argv[1]
manager_primary = mode == "actual-c-primary-manager"
native_mode = "actual-c-primary-same-target" if manager_primary else mode
primary_same_target = native_mode == "actual-c-primary-same-target"
prefix = "pc52-selected-"
if os.getenv("PC_NATIVE_CANCEL_STAGED_SELECTION"):
    prefix += "cancel-staged-"
if mode == "actual-c-primary-same-target" and os.getenv("PC_NATIVE_PRIMARY_EXPECT_RESUME"):
    prefix += "resume-"
if os.getenv("PC_NATIVE_PRIMARY_HOLD_TEARDOWN"):
    prefix += "held-teardown-"
if os.getenv("PC_NATIVE_PRIMARY_CONTROL_LOSS"):
    if not primary_same_target:
        raise SystemExit("control loss requires the same-target fixture")
    prefix += "control-loss-"
root = pathlib.Path(os.environ.get("PC_FIXTURE_ROOT", "/work"))
ready = root / (prefix + mode + "-ready.json")
ready.unlink(missing_ok=True)
env = dict(os.environ, PC_NATIVE_START_READY_FILE=str(ready))
release = None
if env.get("PC_NATIVE_PRIMARY_HOLD_TEARDOWN"):
    release = ready.with_name(ready.stem + "-release")
    release.unlink(missing_ok=True)
    env["PC_NATIVE_PRIMARY_HOLD_TEARDOWN"] = str(release)
if env.get("PC_NATIVE_PRIMARY_CONTROL_LOSS"):
    disconnect = ready.with_name(ready.stem + "-disconnect")
    disconnect.unlink(missing_ok=True)
    env["PC_NATIVE_PRIMARY_CONTROL_LOSS"] = str(disconnect)
    if not manager_primary:
        env["PC_NATIVE_PRIMARY_STOP_RESTART"] = "1"
        env["PC_NATIVE_PRIMARY_NEW_SELECTION"] = "1"
if manager_primary:
    manager_bin = pathlib.Path(env.get("PC_PRIMARY_MANAGER_TEST_BIN", ""))
    if not manager_bin.is_absolute() or not manager_bin.is_file():
        raise SystemExit("missing configured Manager test binary")
    env["PC_NATIVE_PRIMARY_EXPECT_RESUME"] = "1"
else:
    env.pop("PC_PRIMARY_MANAGER_TEST_BIN", None)
if primary_same_target:
    # Real OwnTone HTTP and the START35 harness share the C process and output.
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        primary_port = sock.getsockname()[1]
    env.pop("PODCONNECT_RADIO_SENDER", None)
    env.update(
        PC_NATIVE_PRIMARY_HTTP_PORT=str(primary_port),
        PC_PRIMARY_HTTP_TOKEN="2" * 64,
        PC_REQUIRE_RTP_AUDIO="1",
    )
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
    if primary_same_target:
        endpoint = f"http://127.0.0.1:{primary_port}"
        if info.get("primary_url") != endpoint:
            raise SystemExit("missing same-process production radio HTTP")
        env["PC_NATIVE_PRIMARY_HTTP_URL"] = endpoint
        until = time.monotonic() + 2
        while c.poll() is None and time.monotonic() < until:
            try:
                with socket.create_connection(("127.0.0.1", primary_port), timeout=0.1):
                    break
            except OSError:
                time.sleep(0.025)
        else:
            c.terminate()
            c.wait()
            raise SystemExit("same-process production radio HTTP listener unavailable")
    env.update(
        PC_NATIVE_START_URL=info["url"],
        PC_NATIVE_START_PIPE=info["pipe"],
        PC_NATIVE_START_MODE=native_mode,
    )
    n = subprocess.run(
        [
            os.environ.get("PC_NATIVE_TEST_BIN", "/work/native-tests"),
            "-test.v",
            "-test.timeout=20s",
            "-test.run",
            "^TestPreparedDealerSTARTUsesRealFIFOAndPreservesTransfer/" + native_mode + "$",
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
    if release is not None:
        release.unlink(missing_ok=True)
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
    if (n.returncode or c_status) and primary_same_target:
        markers = re.compile(
            r"FAIL|Manager|Resume|park|barrier|RTP|teardown|unknown|error|"
            r"Primary owner diagnostic:|Radio handoff closed:|"
            r"JSON API request '/api/outputs/[0-9]+/radio-handoff/reacquire'",
            re.IGNORECASE,
        )
        for kind in ("native", "c"):
            log = root / (prefix + mode + f"-{kind}.log")
            lines = [line for line in log.read_text(errors="replace").splitlines() if markers.search(line)]
            for line in lines[-60:]:
                print(re.sub(r"[0-9a-fA-F]{32,}", "[redacted]", line), file=sys.stderr)
    if os.getenv("PC_NATIVE_CANCEL_STAGED_SELECTION") and not n.returncode and not c_status:
        for line in (root / (prefix + mode + "-native.log")).read_text().splitlines():
            if "PASS exact staged decoder cancellation" in line or "actual selected PCM reached C output" in line:
                print(line)
    if os.getenv("PC_NATIVE_PRIMARY_HOLD_TEARDOWN") and not n.returncode and not c_status:
        log = (root / (prefix + mode + "-c.log")).read_text()
        for marker in ("PASS pending park poll preceded exact Spotify TEARDOWN acknowledgment",
                       "PASS same-target authenticated RTP"):
            print(next(line for line in log.splitlines() if line.startswith(marker)))
    if os.getenv("PC_NATIVE_PRIMARY_CONTROL_LOSS") and not n.returncode and not c_status:
        log = (root / (prefix + mode + "-c.log")).read_text().splitlines()
        for marker in ("PASS fixture closed the exact P1 RTSP peer",
                       "PASS control-loss authenticated RTP"):
            print(next(line for line in log if line.startswith(marker)))
    if manager_primary and not n.returncode and not c_status:
        for line in (root / (prefix + mode + "-c.log")).read_text().splitlines():
            if line.startswith(("PASS configured Manager receiver windows", "PASS same-target authenticated RTP")):
                print(line)
    raise SystemExit(
        n.returncode
        or c_status
        or (
            os.getenv("PC_REQUIRE_FIRST_SYNC_CLOCK")
            and (skew_ms is None or skew_ms > 500)
        )
    )

