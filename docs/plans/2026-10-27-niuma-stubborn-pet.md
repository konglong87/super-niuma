# Niuma A — stubborn companion delivery

## Scope and architecture

Approved direction: A, large cream muzzle, carved nostrils, half-lidded eyes,
asymmetric horse ears, swept forelock, squat orange body and dark hooves.
Implement real local GLB geometry, not a billboard or the AI concept image.
Reuse the shared Blender pipeline, existing four named motion joints and asset
registry. Keep `niuma` as default; preserve the previous Niuma bytes as
`niuma-classic`. Go Companion / Chinese Dragon remain unchanged. No credentials,
provider changes, model calls, new dependencies, branches or release publishing.

Initial worktree: clean, HEAD 4955ec6; pulled origin/main before changes.

## Ordered implementation ledger

1. Verified: reproducible sculpt + transparent poster; visual review at large
   and desktop sizes. Files: scripts/assets/build_niuma.py, sculpt helpers,
   web/public/assets/pets/niuma.{glb,png}, niuma-classic.{glb,png}.
2. Verified: asset registry / focused regression tests; validate classic choices,
   GLB geometry, rig origins, resource budget, fallback poster, motion / focus.
3. Verified: full frontend tests + typecheck; build and sign local Wails app;
   actual native clicks, all four model choices, saved default after restart.
4. Verified: real native screenshot for README; update brand/personalization copy,
   review diff, commit each verified slice and push origin/main.

Acceptance: inspect actual model renders and actual native screenshots, not only
DOM assertions. Browser scripted acceptance supplements native acceptance.
Autonomous model task execution is not relevant to a character-only change.
Concept-to-model likeness is an artistic approximation, not pixel equivalence.
Signing/notarization distribution and new releases are deferred.

## Evidence / delivery

Evidence directory: desktop-v2/build/validation/20261027/niuma-stubborn/ (ignored).
Tests, visual review, commits and push results to be recorded after execution.

## Implementation acceptance — 2026-10-27

- Blender 5.2.2 rebuilt the model + transparent 336×396 poster; the ignored
  1000×1000 render was reviewed separately from the native screenshot.
- 579,536-byte GLB, 41 meshes / 27,172 triangles, no external images/textures;
  intentional 768 KiB asset cap accommodates carved/smoothed nose geometry.
- Old Niuma (Classic), Go Companion and Dragon: all six GLB/PNG byte hashes
  match baseline 4955ec6. Both original non-Niuma assets are untouched.
- Focused tests: 3 suites / 11 tests; full frontend: 82 suites / 807 tests;
  typecheck and focused lint pass. Browser E2E: 6 cases on desktop/narrow/mobile,
  including four model choices, moving WebGL frames, focus freeze and drag/hide.
- Native Wails: all four models visibly rendered via real settings clicks;
  save/readback and quit/relaunch preserve Niuma. Preview scale adjusted locally
  from 1 to 1.25 for inspection; shipped default scale remains 1.
- Local build/signature pass using installed Go 1.26.0 with process-scoped
  PATH/GOTOOLCHAIN=local. Default launcher failed because host GOSUMDB is off;
  no global Go configuration or security setting changed.
- No backend code changed, so no new backend test run is claimed. No autonomous
  model calls, live task success, Windows native run or notarization claimed.

Implementation slice committed as bf82665 and pushed to origin/main.
README desktop screenshot replaced with the actual restarted native capture
05-niuma-a-workspace-restarted.jpg, converted to PNG with identical decoded pixels.
README local images resolve; the removed brand-reference sentence stays removed.
Documentation slice reviewed; its final commit/push result is recorded in the
ignored evidence EVIDENCE.md and delivery response. No implementation items remain.

## Follow-up: gentler eyes — 2026-10-27

User feedback: the initial A eyes looked too fierce. Reused the same character,
rigs and generation pipeline; changed only the eye-region geometry/materials:
centered warm-brown pupils, small non-emissive catchlights, thin surface-following
upper lids and softer curved brows. Silhouette, nose, mouth, ears and body remain.

Verified: focused assets/settings tests (11), typecheck, changed-test lint, six
browser E2E cases, local Wails build/signature and actual native settings/workspace
screenshots. Non-eye vertex positions (canonicalized to 1e-5) and node transforms
match the previous model; all six legacy GLB/PNG files remain byte-identical.
README capture refreshed from the native window, without compositing. No backend
or runtime behavior changed; no new release. Evidence and final delivery hashes:
`desktop-v2/build/validation/20261027/niuma-gentle-eyes/` (ignored).
