#!/usr/bin/env node
// Local: node scripts/test-desktop-v2-windows-packaging.mjs
// CI: add --require-pwsh so native-command failure probes cannot be skipped.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const buildPath = "scripts/build-desktop-v2-windows.ps1";
const templatePath = "desktop-v2/build/windows/installer/project.nsi";
const read = (name) => readFileSync(path.join(root, name), "utf8").replaceAll("\r\n", "\n");
const build = read(buildPath);
const template = read(templatePath);
const workflow = read(".github/workflows/desktop-windows.yml");
const nsis = template.replace(/^\s*#.*$/gm, "");
const defines = Object.fromEntries(
  [...nsis.matchAll(/^!define (\w+) "([^"]+)"$/gm)].map((match) => [match[1], match[2]]),
);
const expand = (text) => text.replace(/\$\{(\w+)\}/g, (_, name) => {
  assert.ok(defines[name], `Unresolved NSIS define: ${name}`);
  return defines[name];
});
const pwsh = process.env.PWSH ?? "pwsh";
const hasPowerShell = spawnSync(pwsh, ["-NoProfile", "-Command", "exit 0"]).status === 0;
if (process.argv.includes("--require-pwsh")) {
  assert.ok(hasPowerShell, "PowerShell is required for build failure regression probes");
}

test("NSIS requires the exact staged sidecar beside the installed desktop executable", () => {
  const sidecar = nsis.match(/^\s*File "\/oname=(\$\{SIDECAR_EXECUTABLE\})" "([^"]+)"$/m);
  assert.ok(sidecar, "Sidecar must be a mandatory File, never /nonfatal or a directory-only copy");
  const source = path.resolve(root, path.dirname(templatePath), expand(sidecar[2]).replaceAll("\\", "/"));
  assert.equal(source, path.join(root, "desktop-v2", defines.SIDECAR_EXECUTABLE));
  assert.ok(build.includes(`Join-Path $desktop "${defines.SIDECAR_EXECUTABLE}"`));
  assert.ok(build.includes('"-ldflags", "-s -w"'));
  assert.doesNotMatch(build, /compatibilityBinary/);
  assert.ok(build.includes(`"-o", "${defines.PRODUCT_EXECUTABLE}"`));
  const install = nsis.slice(nsis.indexOf("\nSection\n"), nsis.indexOf("SectionEnd"));
  assert.match(install, /SetOutPath "\$INSTDIR"\s+!insertmacro wails\.files\s+File/);
  assert.doesNotMatch(install.slice(install.indexOf("!insertmacro wails.files")), /SetOutPath/);
  assert.match(nsis, /!ifdef ARG_WAILS_ARM64_BINARY\s+!error/);
});

test("uninstall removes both executables and registration, not user-owned directories", () => {
  const uninstall = nsis.slice(nsis.indexOf('Section "uninstall"'));
  for (const define of ["PRODUCT_EXECUTABLE", "SIDECAR_EXECUTABLE"]) {
    assert.ok(uninstall.includes(`Delete "$INSTDIR\\\${${define}}"`));
  }
  assert.match(uninstall, /!insertmacro wails\.deleteUninstaller\s+RMDir "\$INSTDIR"/);
  assert.doesNotMatch(uninstall, /RMDir\s+\/r|\$AppData|\$LOCALAPPDATA|Delete[^\n]*\*/i);
});

test("build checks native exit codes and selects only the amd64 installer", () => {
  assert.match(build, /& \$Command @Arguments\s+if \(\$LASTEXITCODE -ne 0\) \{\s+throw/);
  for (const command of ["npm", "go", "wails"]) {
    assert.ok(build.includes(`Invoke-CheckedCommand "${command}"`));
    assert.doesNotMatch(build, new RegExp(`^\\s*${command}\\s`, "m"));
  }
  assert.match(build, /\$env:GOOS = "windows"/);
  assert.match(build, /\$env:GOARCH = "amd64"/);
  assert.match(build, /build\\bin\\niuma-amd64-installer\.exe/);
  assert.doesNotMatch(build, /Select-Object -First/);
  assert.match(build, /Get-Command \$command -ErrorAction Stop/);
});

test("Windows CI gates artifacts on payload readback and completed uninstall", () => {
  assert.ok(workflow.includes("scripts/test-desktop-v2-windows-packaging.mjs --require-pwsh"));
  assert.match(workflow, /Get-FileHash \$installed/);
  assert.match(workflow, /Get-FileHash \$payload\[\$name\]/);
  assert.match(workflow, /\/S \/D=\$installDir/);
  assert.match(workflow, /\/S _\?=\$installDir/);
  assert.match(workflow, /Payload survived uninstall/);
  assert.match(workflow, /Uninstall changed user data/);
  assert.ok(workflow.indexOf("Install, read back payload, and uninstall") < workflow.indexOf("Upload installer artifact"));
});

test("SQLite packaging forces CGO and restores the caller environment", () => {
  assert.match(build, /\$savedCGOEnabled = \$env:CGO_ENABLED/);
  const enableCGO = build.indexOf('$env:CGO_ENABLED = "1"');
  assert.ok(enableCGO > 0 && enableCGO < build.indexOf('Invoke-CheckedCommand "go"'));
  assert.match(build, /finally \{[^{}]*\$env:CGO_ENABLED = \$savedCGOEnabled[^{}]*Pop-Location\s*\}\s*$/);
  assert.match(workflow, /Get-Command gcc -CommandType Application -ErrorAction Stop/);
  assert.match(workflow, /-dumpmachine/);
  assert.match(workflow, /x86_64-w64-mingw32/);
  assert.match(workflow, /"CC=\$\(\$compiler\.Source\)" >> \$env:GITHUB_ENV/);
  assert.match(workflow, /go version -m/);
  assert.match(workflow, /Installed sidecar was built without CGO/);
});

// Run the real build script in a disposable repository with fake build tools.
// Failing tools launch a real native process to exercise LASTEXITCODE semantics.
const fixturePrelude = String.raw`
$ErrorActionPreference = "Stop"
$env:VITE_DESKTOP_UI_VERSION = "original-ui"
$env:GOOS = "original-os"
$env:GOARCH = "original-arch"
$originalCGO = $env:PACKAGING_ORIGINAL_CGO
if ($originalCGO -eq "unset") {
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    $originalCGO = $null
} else {
    $env:CGO_ENABLED = $originalCGO
}
$originalCGOPresent = Test-Path Env:CGO_ENABLED
$originalLocation = (Get-Location).Path
function global:makensis {}
function global:Invoke-FixtureTool {
    param([string]$Name, [object[]]$ToolArgs)
    Add-Content (Join-Path $env:PACKAGING_FIXTURE "calls") $Name
    if ($Name -in @("go", "wails") -and $env:CGO_ENABLED -ne "1") {
        throw "SQLite build requires CGO_ENABLED=1"
    }
    if ($env:PACKAGING_CASE -eq $Name) {
        & (Get-Process -Id $PID).Path -NoProfile -Command "exit 23"
        return
    }
    $global:LASTEXITCODE = 0
    switch ($Name) {
        "npm" {
            New-Item "dist" -ItemType Directory -Force | Out-Null
            Set-Content "dist/index.html" "fixture frontend"
        }
        "go" {
            if ($env:GOOS -ne "windows" -or $env:GOARCH -ne "amd64") { throw "Wrong sidecar target" }
            if ($env:PACKAGING_CASE -ne "missing-sidecar") {
                Set-Content $ToolArgs[5] "fixture sidecar"
            }
        }
        "wails" {
            if ($ToolArgs -notcontains "-clean") { throw "Expected Wails clean build" }
            Remove-Item "build/bin" -Recurse -Force
            New-Item "build/bin" -ItemType Directory | Out-Null
            if ($env:PACKAGING_CASE -ne "missing-installer") {
                Set-Content "build/bin/niuma-amd64-installer.exe" "fresh amd64 installer"
            }
            Set-Content "build/bin/unrelated-arm64-installer.exe" "wrong architecture"
        }
    }
}
function global:npm { Invoke-FixtureTool "npm" $args }
function global:go { Invoke-FixtureTool "go" $args }
function global:wails { Invoke-FixtureTool "wails" $args }
try {
    & (Join-Path $env:PACKAGING_FIXTURE "scripts/build-desktop-v2-windows.ps1")
}
finally {
    if ((Get-Location).Path -ne $originalLocation -or
        $env:VITE_DESKTOP_UI_VERSION -ne "original-ui" -or
        $env:GOOS -ne "original-os" -or $env:GOARCH -ne "original-arch" -or
        $env:CGO_ENABLED -ne $originalCGO -or
        (Test-Path Env:CGO_ENABLED) -ne $originalCGOPresent) {
        throw "Build leaked caller environment or working directory"
    }
}
`;

for (const [scenario, calls, originalCGO = "0"] of [
  ["npm", ["npm"]],
  ["go", ["npm", "go"]],
  ["wails", ["npm", "go", "wails"]],
  ["missing-sidecar", ["npm", "go"]],
  ["missing-installer", ["npm", "go", "wails"]],
  ["success", ["npm", "go", "wails"]],
  ["success", ["npm", "go", "wails"], "1"],
  ["success", ["npm", "go", "wails"], "unset"],
  ["go", ["npm", "go"], "unset"],
]) {
  test(`isolated build: ${scenario}, caller CGO=${originalCGO}`, { skip: !hasPowerShell && "pwsh unavailable; Windows CI requires it" }, () => {
    const fixture = mkdtempSync(path.join(tmpdir(), "niuma packaging "));
    const put = (name, text) => {
      const destination = path.join(fixture, name);
      mkdirSync(path.dirname(destination), { recursive: true });
      writeFileSync(destination, text);
    };
    try {
      put(buildPath, build);
      put(templatePath, template);
      put("web/.fixture", "");
      put("desktop-v2/frontend/dist/index.html", "old frontend");
      put("desktop-v2/build/bin/niuma-amd64-installer.exe", "stale installer");
      put("dist/niuma-setup.exe", "previous published installer");
      const result = spawnSync(pwsh, ["-NoProfile", "-NonInteractive", "-Command", fixturePrelude], {
        cwd: fixture,
        env: { ...process.env, PACKAGING_FIXTURE: fixture, PACKAGING_CASE: scenario, PACKAGING_ORIGINAL_CGO: originalCGO },
        encoding: "utf8",
        timeout: 30_000,
      });
      assert.ifError(result.error);
      const output = result.stdout + result.stderr;
      assert.doesNotMatch(output, /Build leaked/);
      assert.equal(result.status === 0, scenario === "success", output);
      assert.deepEqual(readFileSync(path.join(fixture, "calls"), "utf8").trim().split(/\r?\n/), calls);
      const published = readFileSync(path.join(fixture, "dist/niuma-setup.exe"), "utf8").trim();
      assert.equal(published, scenario === "success" ? "fresh amd64 installer" : "previous published installer");
      if (["npm", "go", "wails"].includes(scenario)) {
        assert.match(output, new RegExp(`${scenario} failed with exit code 23`));
      }
      if (scenario === "npm") {
        assert.equal(readFileSync(path.join(fixture, "desktop-v2/frontend/dist/index.html"), "utf8"), "old frontend");
      }
    } finally {
      rmSync(fixture, { recursive: true, force: true });
    }
  });
}
