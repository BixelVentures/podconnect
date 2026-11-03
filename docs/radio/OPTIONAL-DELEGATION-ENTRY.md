# Optional standalone delegation entry

This source-only profile is separate from the existing HA app and S6 launch.
Nothing includes it by default. It does not install, migrate, enable radio, alter
Spotify options, or grant Native authority. Its network is disabled for review.

`scripts/radio-isolated/delegate-entry.sh` factors the tested initial cgroup
setup from the offline factory. It requires a root PID1 with no other visible
process, a private cgroup namespace rooted at its own container leaf, filtered
seccomp, and only the six service-setup capabilities used by the factory. It
rejects administrative capabilities. Exactly one cgroup2 mount may be writable:
`/run/task-cgroup${PODCONNECT_RADIO_DELEGATION_SCOPE}`, where the explicit scope
is a single `/pv-radio-…` component. In a private cgroup namespace the exact
parent bind appears as mount root `/..`; the wrapper requires that exact
one-level parent and the tagged destination. Host receipts must independently
pin the same actual cgroup-parent, host bind source and tagged destination.
A wrong scope tag is rejected, and own-leaf inode matching additionally proves
that this parent actually contains the current namespace root.
`/sys/fs/cgroup` must remain read-only. The wrapper finds its own leaf by inode,
checks root ownership, controllers and sole PID membership, moves only PID1 to
that leaf's new `manager` child, enables cpu/memory/pids on the empty own leaf,
then executes the supplied original entrypoint with its original arguments.
No shared-parent creation, remount, fallback, daemon socket or kernel-admin
capability is used. Any failed check exits before the original entrypoint.

The accompanying `standalone.compose.yaml` is a concrete opt-in offline
profile. An operator must provision a fresh task-owned parent outside this
wrapper and supply the exact host bind path, scope, locally built candidate
image, pinned clone3 seccomp profile and fresh private data directory. It uses
private namespaces, six explicit capabilities, resource limits and no network.
It must never reuse the installed Spotify app or its data directory.

For a future standalone container, first **create without starting** the
container under its unique parent. Inspect that exact container's ID/cgroup
leaf and provision its private `options.json` with the existing
`radio_cgroup_parent` field set to
`/run/task-cgroup<scope>/<actual-container-leaf>` (for example
`/run/task-cgroup/pv-radio-candidate-42/<actual-container-leaf>`).
Keep options and manager ledger owned by the manager and private; the radio
service UID must differ. Only then start that same created container. A recreated
container needs its new exact path configured before starting. No environment
variable replaces this configuration or the manager's own kernel checks.
`PC_RADIO_CHAIN_CGROUP_PARENT` is only the resolved-path fixture diagnostic;
production options are never rewritten by this wrapper.

The Compose command is the original candidate entrypoint (for example its
existing `/init`); the wrapper adds no S6 changes. If the actual original command
has arguments, retain them as separate Compose command items. The executable
must already exist in the locally built candidate. Do not substitute a baseline
image and infer that it contains this branch.

Host-safe checks:

```sh
python3 scripts/radio-isolated/test-delegate-entry.py
```

These cover syntax and refusal/no-change paths, not successful kernel setup.
The offline Docker factory separately exercises the actual wrapper with real
mount/PID/cgroup evidence and inert audio edges. That receipt is required before
claiming this profile kernel-tested. No profile test proves HA/S6 deployment,
network discovery or physical playback. Any future installation and network or
device validation remain separate explicit work.
