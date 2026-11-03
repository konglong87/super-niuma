# Chinese Dragon Desktop Pet Implementation Plan

**Goal:** Add an original Chinese dragon 3D companion, in-window free dragging, and a click menu with close and recovery actions.

**Architecture:** Keep `PetScene` as a pure Three.js presentation component. Register models through a typed catalog, place device-specific position/hidden state behind a small local preference module, and let a new `DesktopPet` component own pointer interaction and menus. Global visual settings continue to own portable appearance choices.

**Tech Stack:** React 19, TypeScript, Three.js, Blender Python, Vitest, Playwright.

---

## Delivery Steps

- [x] Add typed pet catalog and deterministic device preference helpers with unit tests.
- [x] Generate `chinese-dragon.glb` and transparent poster from a reproducible Blender script.
- [x] Remove business clicks from the Three.js renderer and add the `DesktopPet` interaction boundary.
- [x] Add drag persistence, viewport clamping, click menu, close, reset, and settings recovery.
- [x] Add component and browser E2E coverage for model selection, dragging, persistence, menu, and recovery.
- [x] Run typecheck, unit tests, build, Playwright pixel checks, and inspect real screenshots.
- [ ] Remove transient test output, commit with `konglong <konglong@com>`, and push the current branch.

## Verification Commands

```bash
npm --prefix web test -- petAssets petDevicePreferences DesktopPet globalVisualSettings
npm --prefix web run typecheck
npm --prefix web run build
npm --prefix web run test:e2e -- --project chromium-webui-v2-desktop webui-v2.spec.ts settings-v2.spec.ts
```
