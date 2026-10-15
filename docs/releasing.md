# PodConnect — Versioning, Distribution & Updates

PodConnect ships **two cooperating halves from ONE repo** (monorepo):
- **PodConnect Speakers** — the add-on (`podconnect/`), version in `config.yaml`.
- **PodConnect Control** — the integration (`custom_components/podconnect/`), version in `manifest.json`.

## Principles (agreed by review)
1. **Independent SemVer per half. No lockstep.** Tie them together for humans with a shared
   label like *"PodConnect 2026.6"* in changelogs only.
2. **Independent owners and backwards-compatible optional contracts.** Control owns Spotify
   cloud control and HA entities; Speakers owns the local audio engine. Control can optionally
   read the configured Speakers catalogue and request an existing local alias through its
   bounded client. Each half keeps its own version and update path. Preserve legacy service
   replies/descriptors when adding context; expose new contextual reads separately so older
   consumers and either update order keep working.

## How each half updates
- **Integration (HACS):** HACS reads GitHub **Releases** (not `manifest.json`; tags alone aren't
  enough). To ship: bump `manifest.json` `version` → publish a GitHub **Release** whose tag
  equals that version. Pre-releases = betas (hidden unless the user enables "show beta"). Keep
  the manifest version equal to the release tag.
- **Add-on (Add-on Store):** reads `config.yaml` `version` on the default branch and pulls the
  matching GHCR image tag. To ship: **build & push the image to GHCR first**, *then* bump
  `config.yaml` `version` to the same number (CI: `.github/workflows/publish.yaml`).

## Monorepo tag discipline (keeps tags unambiguous)
- **Only the integration gets GitHub Releases.** Every GitHub Release = an integration version.
- **The add-on is versioned via `config.yaml` only — never a GitHub Release** (the add-on store
  ignores releases/tags). This stops HACS from ever offering an "update" with no integration changes.

## Release checklist (per change)
1. Bump **only** the half that changed.
2. Add-on change → CI builds+pushes the GHCR image, then bump `config.yaml` `version` to match.
3. Integration change → bump `manifest.json` `version`, publish a GitHub Release with the matching tag.
4. Update the docs that describe behavior (`CHANGELOG.md` + `README.md`/`TODO.md`/`control-plan.md`
   as relevant — see memory: keep-docs-in-sync-on-release).

## Changelog template
```
PodConnect <Speakers|Control> X.Y.Z  (part of PodConnect 2026.N)
What's new: ...
```
(No "requires the other half" line — the two halves are independent.)

## TODO before submitting to the default HACS store (optional, later)
- Add a brand `icon.png` (a custom-repo install doesn't require it; the default store does).
- Always publish GitHub **Releases** (not just tags) for the integration.

## Native start candidate: exact-source promotion and coupled rollout

For Control0.10.12 / Speakers0.26.19, prepare the complete `podconnect/` subtree
(including the proposed add-on version) on a reviewed branch. Run the image workflow
there with `workflow_dispatch` before merging the add-on version into main. The
build labels each immutable per-architecture image with
`io.bixelventures.podconnect.source-tree = git rev-parse HEAD:podconnect` and its
exact `aarch64`/`amd64` identity. Record each architecture's registry digest.

When that exact subtree reaches main, the image workflow can reuse the existing
image only if its source-tree label, architecture label and actual Linux image
architecture all match. It skips the build/push step. It never overwrites a version.
An existing image with missing/old labels, changed subtree or wrong architecture
fails closed; use a new version for changed source. Registry/auth/network or
malformed inspection failures also fail closed. Only a conclusive missing exact
manifest admits the first build. A merge that changes any file under `podconnect/`
therefore needs another version and reviewed prebuild, not retagging.

Before publishing: independently review promotion code/CI; pass boundary, pinned HA
framework and native manager race/vet checks; complete coupled PodVoice contract
checks; build both native architectures and read back exact labels/digests. Only
then merge the same subtree to advertise Speakers and publish Control's matching
GitHub Release. Keep the image build separate from an installation claim.

### Installation and rollback sequence (requires separately authorized device work)

1. Record current installed AND runtime-loaded versions, add-on image digests,
   existing exact room/output/native IDs and current grants. Take a Home Assistant
   full backup including configuration, custom integrations and add-on data. Keep a
   verified local copy of current `custom_components/podconnect`, PodConnect
   Speakers configuration/data and PodVoice configuration/data plus rollback image
   references. Do not expose credentials in release logs.
2. Coordinate a quiet maintenance window. If music is active, ask the user to stop
   or pause it using the existing controls; release preparation sends no music,
   volume, transfer or test-tone command. Suspend new start requests during update.
3. Update Speakers first to the recorded0.26.19 image. Confirm actual runtime
   version/digest, engine/room/output identity and native start-policy proof with
   read-only checks. Do not infer readiness from an image tag alone.
4. Update Control0.10.12 via HACS, then restart Home Assistant Core to load the new
   custom integration. Confirm runtime version and exact native contract reads;
   stale or missing proof must reject start admission. Restart is an explicit
   maintenance action, not an incidental source-validation step.
5. Update PodVoice30 only after its reviewed composed artifact and grants are ready.
   Confirm runtime version, pairing/session lifecycle and exact native IDs; preserve
   existing grants rather than widening them. Resume normal user-driven controls
   only after read-only admission checks pass. Audible/first-frame start-volume
   acceptance is a separate, explicitly coordinated physical test.
6. On failure, block new starts and roll back the coupled stack, not a single
   optimistic UI version: stop the new PodVoice owner; restore its previous image
   and configuration/grants, restore the previous Control files and restart Core,
   then restore the previous Speakers image/configuration/data. If the old owner
   cannot safely run while newer dependencies are present, keep it stopped until
   all three previous versions are restored. Recheck runtime versions, identities
   and read-only contracts before enabling new starts. Never replay an ambiguous
   interrupted playback request. Restore the full HA backup if targeted rollback
   cannot reproduce the recorded prior state.

This sequence does not authorize installation, grant changes or physical playback.


### Current follow-on candidate: Speakers 0.26.20 / Control 0.10.13

The released-paused URI-start repair changes the native subtree. Build new
0.26.20 architecture images with the exact source-tree labels before merging
that add-on version; never reuse or overwrite 0.26.19. Existing Control 0.10.13
playlist source remains a separately reviewed integration release. Wire proof
and lease schemas are unchanged, but coupled consumer tests and independent
native review are still required. This source record is not an installation.
Use the same backup, quiet maintenance, immutable digest verification, Speakers
then Control then PodVoice order and coupled rollback procedure above, with the
actual reviewed versions substituted in the operator receipt. Green writes
remain with the single installation owner; local native tests send no device
commands. Physical first-audio volume and autoplay still require actual checks.
