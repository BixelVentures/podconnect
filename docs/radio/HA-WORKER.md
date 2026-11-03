# DR radio on standard HA/S6

The existing PodConnect manager remains the execution owner. The existing Native
owner authorizes receiver custody; the radio-role OwnTone process decodes the
closed five-station catalog. Radio never submits tracks to Spotify or uses its
queue as a radio queue. The canonical PodView player renders both sources.

## Behavior

Explicit station + selected room → fresh owner/target/revision admission → durable
operation journal → exact Native receiver handoff → radio decoder and receiver
observation → confirmed player state. Pending is not confirmed playback. Live
radio uses Start/Stop; Start returns to the live stream rather than a paused buffer.

Radio on B must leave Spotify on A playing. Acquiring the same receiver parks its
Spotify source without discarding session, login or queue. Radio Stop does not
automatically resume Spotify. A fresh explicit Spotify request uses the existing
Native handback and drain protocol. Manual volume belongs to the selected source
and receiver; no global volume reset is added.

Failed observation of an active radio source immediately withdraws its controls
and marks UNKNOWN. The existing fence and operation identity remain available for
passive correlated reconciliation. No failure, reload or restart resends an effect.
Unresolved durable custody blocks a replacement sender. A missing or refused radio
worker makes radio unavailable and must not stop the existing Spotify service.

## HA-compatible worker contract

Normal S6 and standard container capabilities are retained. No writable host
cgroup, private delegation, full_access, host PID namespace or Docker socket is
required. The previous optional cgroup backend remains a separate explicit option;
its test evidence is not evidence for this backend.

The fixed root-owned launcher runs permanently as `podradio`, distinct from the
manager and existing `owntone` identity. Before decoder exec it applies:

- Hard 512 MiB virtual-address-space, 32-task, 256-descriptor and zero-core limits.
- No new privileges and a seccomp filter prohibiting process children; pthread
  threads remain possible. Dynamic-loader environment variables are not inherited.
- A ready/go/exec barrier: the decoder cannot start until an independent guardian
  holds the exact pidfd. Manager death or guardian failure stops only that worker.

The guardian samples RSS and process CPU every 100 ms: RSS above 192 MiB or task
excess stops radio; CPU uses a one CPU-second burst allowance replenished at half
a CPU-second per second, stopping sustained excess. This is sampled fail-stop
protection, **not** a cgroup CPU quota or an instantaneous RSS guarantee. The hard
address-space limit remains enforced independently. UID task accounting can make
radio refuse a launch if that UID is already crowded; no limits are applied to
the Spotify process or the overall add-on.

Production readiness checks the real worker, limits, UID/capabilities, seccomp,
pidfd and guardian. One-shot DB initialization additionally requires a successful
joined exit; it does not require a completed initializer to remain alive.

## Identity and stored state

An enabled built-in worker generates private stable keys once in the manager's
0600 `radio-settings.json`. Reads do not create or rotate keys; invalid, linked or
partial state fails closed. Shared HA/Spotify options and login files are not
rewritten. Runtime activation does not assert physical speaker acceptance.

The radio UID cannot read the original Spotify database or Native token. A fixed
trusted early-exit C exporter reads SQLite in read-only/query-only mode and writes
a bounded sanitized receiver stage into a manager-only temporary directory. When
a stage already exists, the original C authority-equality check preserves its
bytes while revalidating controller and receiver keys. Mutable manual volume must
not rotate the import digest. Key/controller changes remain fail-closed. Only the
sanitized stage is transferred to the private radio tree.

The release defaults radio to an idle available service. Only explicit user input
may select a receiver or start audio. Existing explicit `radio_enabled: false`
configuration is respected. Publication, installation and physical acceptance are
recorded separately from the local software evidence.

## Verification

Use the reproducible `scripts/radio-ha-worker` harness for the standard S6 profile
with inert provider/receiver boundaries. Require actual per-scenario execution
markers and current production-source hashes; a scenario name alone is no proof.
The earlier cgroup profile remains under `scripts/radio-isolated` for its own
qualified deployment only. The dated release evidence lists actual results and
retains failed/superseded runs. No local fixture result is household audio proof.
