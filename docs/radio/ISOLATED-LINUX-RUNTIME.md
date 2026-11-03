# Radio runtime and isolated acceptance

The source candidate uses the existing manager, Native actor, dedicated OwnTone
radio sender and signed receiver handoff. Radio is disabled by default. The
ordinary Spotify launcher and its configuration are unchanged. This document
records a Linux/Docker provisioning option; it is not an installation on Home
Assistant Green or acceptance on household speakers.

## Required runtime boundary

The manager must receive a dedicated cgroup-v2 subtree with `cpu`, `memory` and
`pids` delegated. It creates the sender atomically with `clone3`/`CLONE_INTO_CGROUP`,
then checks the actual PID, group membership, limits and guardian. An environment
flag cannot replace these checks. The sender permanently runs as the configured
non-root `owntone` UID/GID, with no effective/permitted/inheritable/ambient
capabilities. It cannot read the manager ledger or Native control options, or
rewrite its kernel budget. Export/import helpers use the same service UID.

The exact `options.json` containing the dedicated Native callback token must be a
regular, singly linked file owned by the manager and private to it (0600 or
stricter). Radio refuses unsafe storage without changing the shared options file.
Existing manager journal storage must also be private and manager-owned;
receipt files must be regular single-link inodes and match the opened file.
Unsafe storage is rejected without changing its ownership, mode or bytes.
The radio directory rejects symlinks and regular hardlinks before ownership
changes or initialization. Pairing import uses its own private database. The
explicit `--radio-pairing-export-known` mode stages configured persisted pairs
even when discovery is unavailable; it skips only absent/unpaired rows and
rejects malformed keys. The older strict export command is unchanged. Stored
pairing never substitutes for current receiver availability or new admission.

Opt-in startup continues bounded passive Native readiness reads with capped
backoff until cancellation. It attempts preparation/launch once. A live idle
sender may await validated discovery; dead, malformed or uncertain custody does
not trigger a replacement process.

A completed Spotify return is retained as the exact authenticated Native
completion receipt, bound to its original hold/source/drain. A clean factory
restart still requires fresh Native/C admission and the same physical pairing
and controller. STOP alone keeps Spotify suspended and cannot unlock restart.
Only the monotone operation counter survives; no source or effect is replayed.

Standard Home Assistant Supervisor app configuration does not expose a dedicated
`cgroup-parent` plus a writable bind of only that subtree. Therefore the existing
HA app cannot claim this runtime merely by enabling radio. No Docker API,
privileged mode, shared writable cgroup root, `SYS_ADMIN`, or automatic migration
of the current Spotify app is introduced. A later installation needs its own
explicitly reviewed provisioning and device checks.

## Offline executable proof

`scripts/radio-isolated/run-factory.py` is an opt-in test runner, not a production
entrypoint. It uses only already-cached images, fresh task-owned containers,
private PID/cgroup namespaces and `--network none`. An inert anchor creates a
unique cgroup parent. Only that exact parent is writable in the fixture. The
manager moves into a child before enabling controllers on its empty container
leaf. Production cgroup/guardian code is unchanged by this provision.

The full fixture drops all container capabilities and adds only `CHOWN`,
`DAC_OVERRIDE`, `FOWNER`, `SETUID`, `SETGID` and `KILL` for service ownership and
cleanup. These are manager/container capabilities; the radio child has zero
capabilities. The earlier clone3-only probe used drop-ALL, but that narrower proof
must not be described as the complete factory runtime.

Docker's default seccomp denies clone3 in this environment. The checked-in
profile at `podconnect/manager/testdata/radio-seccomp/` is pinned to the official
Moby default profile; its sole semantic difference permits clone3. A regression
test compares all other rules to the pinned baseline. There is no non-atomic
spawn-then-move fallback.

Run from the repository root only after building the Linux ARM64 manager test
binary and the explicit source-replay fixtures:

```sh
python3 scripts/radio-isolated/run-factory.py \
  --source-root /absolute/path/to/native-source-replay \
  --output /absolute/path/to/fresh-private-evidence-directory \
  --scenario factory-restart
```

The runner freezes copies of the exact C/CLI/Native/manager binaries before
execution and records their hashes, actual Docker privileges,
exit state and removal of its named containers. It never pulls an image or
contacts a household endpoint. Empty task-owned cgroup-parent directories can
remain in Docker's VM after container cleanup; no processes or resource claims
remain there. Cleanup never requires writing the shared cgroup root.

The C owner fixture retains actual registered receiver keys, SQLite, parsers,
player/source owners, PairVerify, packet transport and drain logic, with finite
audio and inert receiver/discovery edges. The production C CLI performs real
read-only export and initialize-only commands. Native uses its real actor/FIFO
and decoder/provider test edges. This qualifies the composed source path; it is
not proof that an unchanged released image or physical HomePod was tested.

A standalone Linux Docker runtime can supply the same exact subtree and seccomp
profile to a newly built candidate image. Network discovery and real receivers
would then need a separate authorized installation/physical test. Do not reuse
the cached baseline image as though it contained this branch, replace the
installed Spotify app, or enable radio without these actual containment proofs.

References: [Home Assistant app configuration](https://contributors.home-assistant.io/apps/reference/configuration/),
[Docker cgroup-parent](https://docs.docker.com/reference/cli/docker/container/run/),
[Linux cgroup-v2 delegation](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html),
[official Moby profile source](https://github.com/moby/profiles/tree/4b8214fc4971e06172938a1f1554ccff6647ee80).
