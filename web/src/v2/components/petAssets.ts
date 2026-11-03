export const DEFAULT_PET_MODEL = "niuma";

export type PetAsset = {
  url: string;
  poster: string;
  label: { en: string; zh: string };
  camera: { distance: number; fov: number };
  motion?: "niuma" | "chinese-dragon";
};

export const PET_ASSETS = {
  niuma: {
    url: "/assets/pets/niuma.glb",
    poster: "/assets/pets/niuma.png",
    label: { en: "Niuma", zh: "牛马 Niuma" },
    motion: "niuma",
    camera: { distance: 4.5, fov: 32 }
  },
  "niuma-classic": {
    url: "/assets/pets/niuma-classic.glb",
    poster: "/assets/pets/niuma-classic.png",
    label: { en: "Niuma Classic", zh: "牛马经典版" },
    motion: "niuma",
    camera: { distance: 4.5, fov: 32 }
  },
  "go-companion": {
    url: "/assets/pets/go-companion.glb",
    poster: "/assets/pets/go-companion.png",
    label: { en: "Go Companion", zh: "Go 伙伴" },
    camera: { distance: 4.5, fov: 32 }
  },
  "chinese-dragon": {
    url: "/assets/pets/chinese-dragon.glb",
    poster: "/assets/pets/chinese-dragon.png",
    label: { en: "Chinese Dragon", zh: "中国龙" },
    motion: "chinese-dragon",
    camera: { distance: 4.8, fov: 30 }
  }
} as const satisfies Record<string, PetAsset>;

export type PetModel = keyof typeof PET_ASSETS;

export function petAsset(model: string): PetAsset {
  return Object.hasOwn(PET_ASSETS, model) ? PET_ASSETS[model as PetModel] : PET_ASSETS[DEFAULT_PET_MODEL];
}

export function petAssetOptions(language: "en" | "zh"): Array<{ value: PetModel; label: string }> {
  return (Object.entries(PET_ASSETS) as Array<[PetModel, PetAsset]>).map(([value, asset]) => ({
    value,
    label: asset.label[language]
  }));
}
