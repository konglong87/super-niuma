import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { PET_ASSETS } from "./petAssets";

const GLB_MAGIC = 0x46546c67;
const JSON_CHUNK = 0x4e4f534a;

describe("shipped companion assets", () => {
  it("preserves the previous Niuma model and poster byte-for-byte as Classic", () => {
    const checksums = {
      "niuma-classic.glb": "2aeb60a3a41c870239acc6f94018fbce2ea32f919e714c4603a43f16c1aa4f0d",
      "niuma-classic.png": "2bedc28b7167d7a50deb6505321f22b02eb91edbad281485b7e53ff2c7346dc2"
    };
    for (const [file, sha256] of Object.entries(checksums)) {
      expect(createHash("sha256").update(readFileSync(resolve("public/assets/pets", file))).digest("hex")).toBe(sha256);
    }
  });
  for (const [id, asset] of Object.entries(PET_ASSETS)) {
    it(`${id} has a valid local GLB container and transparent PNG poster`, () => {
      const glb = readFileSync(resolve("public", asset.url.slice(1)));
      expect(glb.readUInt32LE(0)).toBe(GLB_MAGIC);
      expect(glb.readUInt32LE(4)).toBe(2);
      expect(glb.readUInt32LE(8)).toBe(glb.length);
      expect(glb.readUInt32LE(16)).toBe(JSON_CHUNK);
      const doc = JSON.parse(glb.subarray(20, 20 + glb.readUInt32LE(12)).toString());
      expect(doc.meshes.length).toBeGreaterThan(0);
      expect(doc.buffers.every((buffer: { uri?: string }) => !buffer.uri)).toBe(true);
      if (id === "niuma") {
        expect(glb.length).toBeLessThan(768 * 1024);
        const names = doc.nodes.map((node: { name: string }) => node.name);
        expect(names).not.toContain("Beacon");
        expect(names).not.toContain("ChestLight");
        for (const feature of ["SculptedMuzzle", "SeamlessBody", "Horn_-1", "Horn_1", "Forelock_0", "RelaxedLid_-1", "RelaxedLid_1", "Catchlight_-1", "Catchlight_1", "EarOuter_1", "MintBadge"]) {
          // Curve-only features must not disappear during glTF export.
          expect(doc.nodes.find((node: { name: string }) => node.name === feature)?.mesh).toBeTypeOf("number");
        }
        expect(doc.images ?? []).toHaveLength(0); // Actual geometry, no raster billboard.
        expect(doc.materials.every((material: { emissiveFactor?: number[] }) =>
          !material.emissiveFactor?.some((value) => value > 0))).toBe(true);
        for (const joint of ["HeadRig", "TailRig", "LeftArmRig", "RightArmRig"]) {
          const node = doc.nodes.find((item: { name: string }) => item.name === joint);
          expect(node).toBeDefined();
          expect(node.children.length).toBeGreaterThan(0);
          expect(node.translation.some((axis: number) => axis !== 0)).toBe(true);
        }
      }
      const poster = readFileSync(resolve("public", asset.poster.slice(1)));
      expect(poster.subarray(0, 8).toString("hex")).toBe("89504e470d0a1a0a");
      expect(poster.readUInt32BE(16)).toBe(336);
      expect(poster.readUInt32BE(20)).toBe(396);
      expect(poster[25]).toBe(6); // RGBA
    });
  }
});
