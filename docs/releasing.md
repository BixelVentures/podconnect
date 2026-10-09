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
