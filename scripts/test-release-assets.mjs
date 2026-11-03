#!/usr/bin/env node
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const script = path.join(root, "scripts/verify-release-assets.sh");
const version = "v0.1.0";
const assets = [
  `niuma_${version}_darwin_arm64.tar.gz`,
  `niuma_${version}_darwin_amd64.tar.gz`,
  `niuma_${version}_linux_amd64.tar.gz`,
  `niuma_${version}_linux_arm64.tar.gz`,
  `niuma_${version}_windows_amd64.zip`,
  `niuma-${version}-macos-arm64.dmg`,
  `niuma-${version}-macos-amd64.dmg`,
  `niuma-desktop_${version}_linux_amd64.tar.gz`,
  "niuma-setup.exe",
];

const run = (directory) => spawnSync("bash", [script, version, directory], { encoding: "utf8" });
const fixture = mkdtempSync(path.join(tmpdir(), "niuma-release-assets-"));
try {
  mkdirSync(fixture, { recursive: true });
  const manifest = [];
  for (const asset of assets) {
    const content = `fixture:${asset}\n`;
    writeFileSync(path.join(fixture, asset), content);
    const digest = createHash("sha256").update(content).digest("hex");
    manifest.push(`${digest}  ${asset}`);
  }
  writeFileSync(path.join(fixture, "SHA256SUMS"), `${manifest.join("\n")}\n`);
  writeFileSync(
    path.join(fixture, "RELEASE_NOTES.md"),
    ["## 新增功能", "## 修复问题", "## 安装方式", "## 已知问题", "## 系统要求", ""].join("\n"),
  );

  const valid = run(fixture);
  assert.equal(valid.status, 0, `${valid.stdout}\n${valid.stderr}`);

  writeFileSync(path.join(fixture, assets[0]), "tampered");
  assert.notEqual(run(fixture).status, 0, "tampered payload must fail");
  writeFileSync(path.join(fixture, assets[0]), `fixture:${assets[0]}\n`);

  writeFileSync(path.join(fixture, "SHA256SUMS"), `${manifest.join("\n")}\n${manifest[0]}\n`);
  assert.notEqual(run(fixture).status, 0, "duplicate checksum must fail");
  writeFileSync(path.join(fixture, "SHA256SUMS"), `${manifest.slice(1).join("\n")}\n`);
  assert.notEqual(run(fixture).status, 0, "missing checksum must fail");
  writeFileSync(path.join(fixture, "SHA256SUMS"), `${manifest.join("\n")}\n`);

  writeFileSync(path.join(fixture, "niuma-extra.zip"), "unexpected");
  assert.notEqual(run(fixture).status, 0, "extra asset must fail");
  rmSync(path.join(fixture, "niuma-extra.zip"));

  rmSync(path.join(fixture, assets[7]));
  const invalid = run(fixture);
  assert.notEqual(invalid.status, 0);
  assert.match(invalid.stderr, /missing or empty asset/);
  console.log("Release assets: valid set accepted; tampering, duplicate, missing and extra files rejected");
} finally {
  rmSync(fixture, { recursive: true, force: true });
}
