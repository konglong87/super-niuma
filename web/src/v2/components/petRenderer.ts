import {
  AmbientLight, Box3, DirectionalLight, Group, Mesh, PerspectiveCamera,
  Scene, SRGBColorSpace, Vector3, WebGLRenderer, type Material, type Object3D
} from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import type { PetAsset } from "./petAssets";

const FIT_VIEW_SIZE = 1.9;
const PET_RIG_NAMES = ["HeadRig", "TailRig", "LeftArmRig", "RightArmRig"] as const;
type PetRigName = typeof PET_RIG_NAMES[number];

type PetRigBase = {
  node: Object3D;
  x: number;
  y: number;
  z: number;
};

function disposeModel(model: Object3D): void {
  const materials = new Set<Material>();
  model.traverse((node) => {
    if (!(node instanceof Mesh)) return;
    node.geometry.dispose();
    for (const material of Array.isArray(node.material) ? node.material : [node.material]) materials.add(material);
  });
  for (const material of materials) material.dispose();
}

export function createPetRenderer(canvas: HTMLCanvasElement, asset: PetAsset, getAnimation: () => string, onReady: () => void, onError: () => void): () => void {
  const renderer = new WebGLRenderer({ canvas, alpha: true, antialias: true, preserveDrawingBuffer: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
  renderer.setSize(112, 132, false);
  renderer.outputColorSpace = SRGBColorSpace;
  const scene = new Scene();
  const camera = new PerspectiveCamera(asset.camera.fov, 112 / 132, 0.1, 20);
  camera.position.set(0.35, 0.3, asset.camera.distance);
  camera.lookAt(0, 0, 0);
  scene.add(new AmbientLight(0xffffff, 2));
  const key = new DirectionalLight(0xffffff, 3.5);
  key.position.set(3, 5, 4);
  scene.add(key);
  const fill = new DirectionalLight(0xffffff, 1.5);
  fill.position.set(-3, 1, -2);
  scene.add(fill);
  const pivot = new Group();
  scene.add(pivot);
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  let closed = false;
  let model: Object3D | undefined;
  const rigs: Partial<Record<PetRigName, PetRigBase>> = {};
  const isNiuma = asset.motion === "niuma";
  const isChineseDragon = asset.motion === "chinese-dragon";
  let frame = 0;
  const start = performance.now();
  const draw = (now: number): void => {
    if (closed) return;
    const time = (now - start) / 1000;
    const animation = getAnimation();
    const moving = !reducedMotion.matches && animation !== "focus";
    const energy = animation === "celebrate" ? 1.7 : 1;
    pivot.rotation.y = moving ? Math.sin(time * 1.4) * 0.12 * energy : 0;
    pivot.rotation.x = moving ? Math.sin(time * 1.8 + 0.6) * 0.025 * energy : 0;
    pivot.rotation.z = moving ? Math.sin(time * 1.15 + 1.2) * 0.035 * energy : 0;
    pivot.position.y = moving ? Math.sin(time * (animation === "celebrate" ? 5 : 2)) * 0.035 : 0;
    const breath = moving ? 1 + Math.sin(time * 2.2) * 0.012 : 1;
    pivot.scale.setScalar(breath);
    if (isChineseDragon || isNiuma) {
      const rigMotion = moving ? energy : 0;
      const setRigRotation = (name: PetRigName, x: number, y: number, z: number): void => {
        const base = rigs[name];
        if (!base) return;
        base.node.rotation.set(base.x + x * rigMotion, base.y + y * rigMotion, base.z + z * rigMotion);
      };
      if (isNiuma) {
        setRigRotation("HeadRig", Math.sin(time * 1.9 + 0.3) * 0.025, Math.sin(time * 1.25) * 0.02, Math.sin(time * 1.35) * 0.035);
        setRigRotation("TailRig", Math.sin(time * 1.8) * 0.04, 0, Math.sin(time * 2.2 - 0.8) * 0.18);
        setRigRotation("LeftArmRig", animation === "celebrate" ? -0.16 + Math.sin(time * 3.7) * 0.06 : Math.sin(time * 1.7) * 0.025, 0, 0);
        setRigRotation("RightArmRig", animation === "celebrate" ? 0.16 + Math.sin(time * 3.7) * 0.06 : Math.sin(time * 1.55 + 1.2) * 0.025, 0, 0);
      } else {
        setRigRotation("HeadRig", Math.sin(time * 1.9 + 0.3) * 0.035, Math.sin(time * 1.25) * 0.018, Math.sin(time * 1.35) * 0.055);
        setRigRotation("TailRig", 0, 0, Math.sin(time * 2.0 - 0.8) * 0.22);
        setRigRotation(
          "LeftArmRig",
          animation === "celebrate" ? -0.13 + Math.sin(time * 3.7) * 0.05 : Math.sin(time * 1.7) * 0.025,
          0,
          animation === "celebrate" ? -0.33 + Math.sin(time * 3.7) * 0.08 : Math.sin(time * 1.7 + 0.5) * 0.035,
        );
        setRigRotation("RightArmRig", 0, 0, Math.sin(time * 1.55 + 1.2) * 0.025);
      }
    }
    if (!document.hidden && canvas.getClientRects().length > 0) renderer.render(scene, camera);
    frame = window.requestAnimationFrame(draw);
  };
  const contextLost = (event: Event): void => { event.preventDefault(); onError(); };
  canvas.addEventListener("webglcontextlost", contextLost);
  new GLTFLoader().load(asset.url, (gltf) => {
    if (closed) { disposeModel(gltf.scene); return; }
    model = gltf.scene;
    const box = new Box3().setFromObject(model);
    const size = box.getSize(new Vector3());
    const maxDimension = Math.max(size.x, size.y, size.z);
    if (maxDimension > 0) model.scale.setScalar(FIT_VIEW_SIZE / maxDimension);
    const center = new Box3().setFromObject(model).getCenter(new Vector3());
    model.position.sub(center);
    if (isChineseDragon || isNiuma) {
      for (const name of PET_RIG_NAMES) {
        const node = model.getObjectByName(name);
        if (!node) continue;
        const rig = { node, x: node.rotation.x, y: node.rotation.y, z: node.rotation.z };
        rigs[name] = rig;
      }
    }
    pivot.add(model);
    renderer.render(scene, camera);
    onReady();
    frame = window.requestAnimationFrame(draw);
  }, undefined, () => { if (!closed) onError(); });
  return () => {
    closed = true;
    window.cancelAnimationFrame(frame);
    canvas.removeEventListener("webglcontextlost", contextLost);
    if (model) disposeModel(model);
    renderer.dispose();
  };
}
