# PodConnect — Versioning, Distribution & Updates

PodConnect ships **two cooperating halves from ONE repo** (monorepo):
- **PodConnect Speakers** — the add-on (`podconnect/`), version in `config.yaml`.
- **PodConnect Control** — the integration (`custom_components/podconnect/`), version in `manifest.json`.

## Principles (agreed by review)
1. **Independent SemVer per half. No lockstep.** Tie them together for humans with a shared
   label like *"PodConnect 2026.6"* in changelogs only.
2. **The integration's core (Spotify cloud control) works WITHOUT the add-on.** The add-on only
   *enhances* HomePod features (instant state, real volume). The integration must **degrade,
   never crash** when the add-on is old/stopped/absent.
3. **Compatibility via a versioned contract + capability detection — not version-matching.**
   The integration talks to the add-on only through a stable facade (the manager's
   `/podconnect/info` + events websocket + volume), never to go-librespot/OwnTone directly.
   go-librespot (3678) / OwnTone (3689) are private internals. (Built in Phase 2+; see
   `docs/CONTRACT.md` when it exists.)
4. **Mismatches surface as a friendly HA Repairs card** that names the exact button to click and
   auto-clears once fixed. The user never reads a compatibility matrix or plans an update order.

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
4. Keep backward compatibility with the currently-released partner for **≥1 release**, so update
   order never matters.
5. Release notes (template below). If the wire contract changed, bump `contract_version` + update
   `docs/CONTRACT.md`.

## Changelog template
```
PodConnect <Speakers|Control> X.Y.Z  (part of PodConnect 2026.N)
Requires: PodConnect <other half> >= A.B      ← always first
What's new: ...
Do you need to update the other half? Yes / No
Safe to update anytime — HA shows a repair card if the halves ever mismatch.
```

## Discovery of the add-on by the integration (future, Phase 2+)
Supervisor API (same-host, zero-config) → zeroconf (`_podconnect-mgr._tcp`) → manual host:port.
All confirm via the manager's `/podconnect/info` (source of truth for `contract_version` + capabilities).

## TODO before submitting to the default HACS store (optional, later)
- Add a brand `icon.png` (a custom-repo install doesn't require it; the default store does).
- Always publish GitHub **Releases** (not just tags) for the integration.
