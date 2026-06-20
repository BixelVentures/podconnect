# PodConnect — Versioning, Distribution & Updates

PodConnect ships **two cooperating halves from ONE repo** (monorepo):
- **PodConnect Speakers** — the add-on (`podconnect/`), version in `config.yaml`.
- **PodConnect Control** — the integration (`custom_components/podconnect/`), version in `manifest.json`.

## Principles (agreed by review)
1. **Independent SemVer per half. No lockstep.** Tie them together for humans with a shared
   label like *"PodConnect 2026.6"* in changelogs only.
2. **The two halves are fully decoupled.** Control (Spotify cloud control) and Speakers (the local
   audio engine) are **independent** — Control never talks to the add-on at all (the brief attempt to
   add local-speaker entities was reverted in 0.7.1). So there's no Speakers↔Control wire contract to
   stabilize, and no update-order to worry about: either half updates on its own.
   *(The old "versioned contract / `docs/CONTRACT.md` facade" idea is moot — kept out of scope.)*

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
