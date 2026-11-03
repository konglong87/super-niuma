import { describe, expect, it } from "vitest";
import { PET_ASSETS, petAsset, petAssetOptions } from "./petAssets";

describe("pet assets", () => {
  it("registers Niuma, the robot and Chinese dragon with reproducible GLB and poster assets", () => {
    expect(Object.keys(PET_ASSETS)).toEqual(["niuma", "niuma-classic", "go-companion", "chinese-dragon"]);
    expect(PET_ASSETS.niuma).toMatchObject({
      url: "/assets/pets/niuma.glb",
      poster: "/assets/pets/niuma.png",
      label: { en: "Niuma", zh: "牛马 Niuma" }
    });
    expect(PET_ASSETS["chinese-dragon"]).toMatchObject({
      url: "/assets/pets/chinese-dragon.glb",
      poster: "/assets/pets/chinese-dragon.png",
      label: { en: "Chinese Dragon", zh: "中国龙" }
    });
  });

  it("falls back to the default model and localizes selection options", () => {
    for (const unknown of ["missing", "", "constructor", "__proto__"]) {
      expect(petAsset(unknown)).toBe(PET_ASSETS.niuma);
    }
    expect(petAsset("niuma-classic")).toBe(PET_ASSETS["niuma-classic"]);
    expect(petAsset("go-companion")).toBe(PET_ASSETS["go-companion"]);
    expect(petAsset("chinese-dragon")).toBe(PET_ASSETS["chinese-dragon"]);
    expect(petAssetOptions("zh")).toEqual([
      { value: "niuma", label: "牛马 Niuma" },
      { value: "niuma-classic", label: "牛马经典版" },
      { value: "go-companion", label: "Go 伙伴" },
      { value: "chinese-dragon", label: "中国龙" }
    ]);
  });
});
