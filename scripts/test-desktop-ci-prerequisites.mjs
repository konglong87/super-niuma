#!/usr/bin/env node
// Requires Git, Node and the repository's Go toolchain; no npm install or GUI.
// Tests copy only Git-visible source into a disposable directory. Local web/dist
// and previously packaged desktop assets cannot make the fresh-clone check pass.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const desktops = ["desktop-v2"];
const go = process.env.GO ?? "go";
const run = (command, args, cwd = root, env = {}) => {
  const result = spawnSync(command, args, {
    cwd, env: { ...process.env, ...env }, encoding: "utf8", timeout: 120_000,
    maxBuffer: 10 * 1024 * 1024,
  });
  assert.ifError(result.error);
  return result;
};
const checked = (command, args, cwd = root, env = {}) => {
  const result = run(command, args, cwd, env);
  assert.equal(result.status, 0, `${command} ${args.join(" ")}\n${result.stdout}${result.stderr}`);
  return result.stdout;
};

test("only embed placeholders, not generated frontend payloads, are Git-visible", () => {
  for (const desktop of desktops) {
    const placeholder = `${desktop}/frontend/dist/.gitkeep`;
    assert.equal(run("git", ["check-ignore", "--quiet", "--no-index", placeholder]).status, 1, placeholder);
    for (const generated of ["index.html", "assets/index.js"]) {
      assert.equal(run("git", ["check-ignore", "--quiet", "--no-index", `${desktop}/frontend/dist/${generated}`]).status, 0);
    }
  }
});

test("fresh source embeds only placeholders and passes desktop build, vet and unit tests", () => {
  const fixture = mkdtempSync(path.join(tmpdir(), "niuma-fresh-desktop-"));
  try {
    const files = checked("git", [
      "ls-files", "--cached", "--others", "--exclude-standard", "-z", "--",
      "go.mod", "go.sum", "third_party", "internal", ...desktops,
    ]).split("\0").filter(Boolean);
    for (const file of new Set(files)) {
      const destination = path.join(fixture, file);
      mkdirSync(path.dirname(destination), { recursive: true });
      copyFileSync(path.join(root, file), destination);
    }
    const packages = desktops.map((desktop) => `./${desktop}`);
    const env = { GOWORK: "off", GOFLAGS: "", CGO_ENABLED: "1" };
    const embedded = packages.map((pkg) =>
      JSON.parse(checked(go, ["list", "-json=EmbedFiles", pkg], fixture, env)).EmbedFiles);
    assert.deepEqual(embedded, desktops.map(() => ["frontend/dist/.gitkeep"]));
    const buildOutput = path.join(fixture, "desktop-v2-test-bin");
    checked(go, ["build", "-o", buildOutput, ...packages], fixture, env);
    checked(go, ["vet", ...packages], fixture, env);
    checked(go, ["test", ...packages, "-count=1"], fixture, env);

    // Verify the pinned Wails dependency selection, without a Linux compiler:
    // ordinary checks use no GUI libraries; production requires WebKit 4.1.
    const linuxEnv = { ...env, GOOS: "linux", GOARCH: "amd64" };
    const deps = ["-deps", "-f", '{{join .CgoPkgConfig " "}}', ...packages];
    assert.doesNotMatch(checked(go, ["list", ...deps], fixture, linuxEnv), /gtk|webkit/);
    const nativeDeps = checked(go, ["list", "-tags=production,webkit2_41", ...deps], fixture, linuxEnv);
    assert.match(nativeDeps, /gtk\+-3\.0/);
    assert.match(nativeDeps, /webkit2gtk-4\.1/);
    assert.doesNotMatch(nativeDeps, /webkit2gtk-4\.0/);

    // Negative control: reproduce the original missing embed directory failure.
    for (const desktop of desktops) {
      rmSync(path.join(fixture, desktop, "frontend/dist"), { recursive: true });
    }
    const missing = run(go, ["list", ...packages], fixture, env);
    assert.notEqual(missing.status, 0);
    assert.match(missing.stderr, /pattern all:frontend\/dist: no matching files found/);
  } finally {
    rmSync(fixture, { recursive: true, force: true });
  }
});

test("desktop CI separates source-only checks from real native builds and follows main", () => {
  const ci = readFileSync(path.join(root, ".github/workflows/ci.yml"), "utf8");
  const quality = readFileSync(path.join(root, ".github/workflows/quality.yml"), "utf8");
  const nativeWorkflow = readFileSync(
    path.join(root, ".github/workflows/desktop-linux.yml"),
    "utf8",
  );
  const native = nativeWorkflow.slice(nativeWorkflow.indexOf("  native:"));
  assert.doesNotMatch(ci, /desktop-linux:/);
  assert.match(native, /runs-on: ubuntu-24\.04/);
  assert.match(nativeWorkflow, /CGO_ENABLED: '1'/);
  assert.match(nativeWorkflow, /branches: \[main\]/);
  assert.match(nativeWorkflow, /paths:/);
  assert.match(native, /bash scripts\/build-desktop-v2\.sh -platform linux\/amd64 -tags webkit2_41/);
  assert.match(native, /libgtk-3-dev libwebkit2gtk-4\.1-dev/);
  assert.match(native, /npm ci --legacy-peer-deps/);
  assert.match(native, /wails@v2\.10\.2/);
  assert.match(quality, /node --test scripts\/test-desktop-ci-prerequisites\.mjs/);
  const windows = readFileSync(path.join(root, ".github/workflows/desktop-windows.yml"), "utf8");
  assert.match(windows, /branches:\s+- main/);
  assert.match(windows, /workflow_call:/);
  assert.match(windows, /version:/);
  assert.match(windows, /name: release-windows-/);
  const release = readFileSync(path.join(root, ".github/workflows/release.yml"), "utf8");
  assert.match(release, /push:\s+tags:\s+- "v\*"/);
  assert.match(release, /package-desktop-v2-linux\.sh/);
  assert.match(release, /package-desktop-v2-macos\.sh/);
  assert.match(release, /macos-15/);
  assert.match(release, /macos-15-intel/);
  assert.match(release, /APPLE_CERTIFICATE_P12_BASE64/);
  assert.match(release, /signing_mode/);
  assert.match(release, /Configure all six Apple Secrets/);
  assert.match(release, /MACOS_RELEASE_MODE/);
  assert.match(release, /skipping code-signature and Gatekeeper verification/);
  assert.match(release, /sha256sum niuma-\* niuma_\*/);
  assert.match(release, /gh release create/);
  assert.match(release, /gh release download/);
  assert.match(release, /acceptance-linux:/);
  assert.match(release, /acceptance-macos:/);
  assert.match(release, /acceptance-windows:/);
  assert.match(release, /spctl --assess/);
  assert.match(release, /Start-Process \$installer/);
  assert.match(release, /verify-release-assets\.sh/);
  assert.match(release, /name: verified-preview-/);
  assert.match(release, /GITHUB_EVENT_NAME.*== push/);
  assert.match(release, /TAG: \$\{\{ github\.ref_name \}\}/);
  assert.match(release, /assets=\(release-dist\/niuma-\* release-dist\/niuma_\* release-dist\/SHA256SUMS release-dist\/RELEASE_NOTES\.md\)/);
  const linuxPackage = readFileSync(path.join(root, "scripts/package-desktop-v2-linux.sh"), "utf8");
  assert.match(linuxPackage, /STAGE="niuma-desktop_\$\{VERSION\}_linux_\$\{ARCH\}"/);
  const macosPackage = readFileSync(path.join(root, "scripts/package-desktop-v2-macos.sh"), "utf8");
  assert.match(macosPackage, /Contents\/MacOS\/niuma-desktop/);
  assert.match(macosPackage, /xcrun notarytool submit/);
  assert.match(macosPackage, /xcrun stapler staple/);
  const releaseDocs = readFileSync(path.join(root, "docs/deployment/release_pipeline.md"), "utf8");
  assert.match(releaseDocs, /未签名\/未公证/);
  assert.match(releaseDocs, /系统设置 → 隐私与安全性 → 安全性/);
});
