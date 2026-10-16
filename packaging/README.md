# Native22 / Control15 packaging draft (unpublished)

Exact Git base: `2922d391fb2dfc9d1992f957334b77bcb6ee6f72`, matching public `main` at verification time. The Control/manager source projection applies the 11 manifest-pinned patches in order: seven foundation patches, START35 manager/control, then MOVE manager/control. Assets `08`–`11` preserve the four final deltas byte-for-byte from `CONTROL-MANAGER-SOURCE-FREEZE.json`.

All 60 foundation source pins matched before applying START35/MOVE; afterward all 101 Control/manager paths match that freeze's `final_sources.files`. Existing native/OwnTone source stacks remain pinned and reconstructed separately. The current Dockerfile retains the published native/OwnTone patches and adds the reviewed AUDIO4/volume/burst foundation stack. The final START35/MOVE native/C mutator stack is not integrated, so this draft is not an image-ready composition or a mixed-version readiness claim.

Development commits and branch pushes preserve this checkpoint; they do not publish or install it. No build/test rerun, version edit, publication, provider, or Green operation occurred. The source freeze records no physical result.
