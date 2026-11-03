# Actual standard-HA worker composition harness

This is test infrastructure. It does not install, restart HA, contact a provider,
or send to household receivers. Docker network is `none`; the C fixture supplies
finite offline provider data and inert loopback receivers. Actual C decoder,
pairing, player, crypto/RTP, Native, manager, worker helper, pidfd, seccomp,
limits, guardian and durable journals execute together.

The driver requires ordinary Docker defaults and S6 `/init`, uses canonical
`with-contenv`, and inspects the resulting profile. It rejects privilege,
custom caps/seccomp, host PID, a cgroup parent or any cgroup mount. Test-only S6
bundles mask unrelated shipping services; this proves the composition under S6,
not the complete shipping image's normal services. Shipping startup and actual
production C idle qualification are separate evidence.

`fixture-exec.c` replaces `/usr/sbin/owntone` **only in the test image**. It uses
same-PID exec, never fork. Actual production C handles trusted pairing export and
initialize-only. Runtime execs the explicitly named C test fixture and restores
only nonsecret `PC_RADIO_HTTP_*` values from the read-only fixture env file.
The unchanged production worker helper still applies all real restrictions.

Prepare a private working directory using the identified candidate manager:

```sh
python3 prepare-manager.py --source-manager /absolute/candidate/podconnect/manager --runtime-root /absolute/runtime
```

This copies source, hashes every source file, applies `radio-worker-test-overlay.patch`
and asserts all non-test files remain byte-identical. Compile its manager tests
with the candidate Go version (`go test -c`); put the result at
`runtime/bin/go-radio-full-chain.test`. Compile the candidate's exact
`podconnect/radio-worker/worker.c` to `runtime/bin/podconnect-radio-worker`, and
compile this directory's `fixture-exec.c` to `runtime/bin/fixture-exec`.
Copy the build image's `/bin/false` to `runtime/bin/worker-unavailable`.

The test-only Dockerfile adds the dedicated `podradio` user, exact worker helper
and explicit exec adapter to the identified compatible runtime image. Native/C
composition outputs must have `bin/native-fixture64`, `bin/owntone-fixture`,
`bin/owntone` and their exact C source/build tree at `owntone/` (SQLite extension
uses its actual `/work` compile path, mounted read-only). The Native fixture must
accept 32/64-character fixture tokens and explicit `EXPECT_RETURN=unchanged`.
These are test-only expectation changes, not changes to owner authorization.

Run each scenario **sequentially** to avoid interference between independent
containers sharing the same host service UID's `RLIMIT_NPROC` accounting:

```sh
python3 run-worker.py --runtime-root /absolute/runtime --composition-root /absolute/native-c-composition --image radio-v2x-worker-runtime:local --scenario start-stop
```

Scenarios: `start-stop`, `factory-restart`, `move-roundtrip`, `move-coexist`,
`selected`, `discovery`, `evolution`, `unknown`, `unavailable`.
Every named scenario requires its specific observed log markers; a generic test
PASS cannot stand in for an unexecuted branch. The driver captures source and
artifact hashes, actual container configuration, logs, synthetic private radio
state for diagnosis, and cleanup. Private fixture state contains only synthetic
receiver keys; do not print or reuse it as household pairing material.

`unknown` kills and joins the real sender, requires first observed UNKNOWN,
blocked restored producer and no passive replay. `unavailable` mounts an actual
failing helper and requires unchanged real Native identity/revision; Native's
final assertion independently requires the original live source/session/queue,
open writer and no radio hold. Physical acceptance is always false.
