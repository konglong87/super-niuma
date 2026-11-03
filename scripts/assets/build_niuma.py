"""Build the approved A / stubborn Niuma as actual, self-contained GLB meshes.

Run: blender --background --python-exit-code 1 --python scripts/assets/build_niuma.py
No downloaded model, generated raster billboard, textures or external services.
"""

import sys
from pathlib import Path

import bpy

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from pet_pipeline import reset_scene, make_material, ellipsoid, export_glb, render_poster
from pet_sculpt import empty, rounded_mass, carve_ellipsoid, tapered_stroke, leaf, localize_joint, fuse_masses, trim_local_z

# Linear material values: deliberately darker than the previous pastel robot.
COLORS = {
    "orange": (0.43, 0.105, 0.025),
    "ear_inner": (0.32, 0.060, 0.023),
    "cream": (0.70, 0.57, 0.38),
    "eye": (0.86, 0.82, 0.68),
    "eye_detail": (0.065, 0.038, 0.022),
    "catchlight": (0.94, 0.90, 0.82),
    "charcoal": (0.035, 0.028, 0.025),
    "cavity": (0.095, 0.057, 0.030),
    "mint": (0.25, 0.65, 0.46),
}
ROOT_NAME = "NiumaPet"
OUTPUT_MODEL = "niuma.glb"
OUTPUT_POSTER = "niuma.png"
PREVIEW_DIRECTORY = Path(__file__).resolve().parents[2] / "output/imagegen/niuma-stubborn-model"
SIDES = (-1, 1)


def build_body(root, arms, materials):
    orange, dark = materials["orange"], materials["charcoal"]
    body_parts = [rounded_mass("PearBody", (0, .10, 1.28), (.72, .50, .91), orange, root, .95)]
    for side, arm in zip(SIDES, arms):
        body_parts.append(ellipsoid(f"Haunch_{side}", (side * .40, .06, .62), (.29, .33, .42), orange, root))
        body_parts.append(rounded_mass(f"Shin_{side}", (side * .41, .01, .40), (.22, .24, .29), orange, root))
        # Two rounded halves make a real cloven hoof silhouette.
        for half in SIDES:
            rounded_mass(f"Hoof_{side}_{half}", (side * .43 + half * .125, -.11, .17),
                         (.123, .32, .14), dark, root, .65, segments=20, rings=12)
        tapered_stroke(f"Arm_{side}", [(side * .52, .04, 1.93), (side * .70, -.05, 1.50),
                       (side * .77, -.19, 1.08)], [1, 1, .82], .18, orange, arm)
        rounded_mass(f"Hand_{side}", (side * .78, -.21, .99), (.18, .19, .20), dark, arm, .72,
                     segments=20, rings=12)
    fuse_masses(body_parts, "SeamlessBody", root)
    rounded_mass("MintBadge", (.30, -.388, 1.78), (.072, .035, .073),
                 materials["mint"], root, .45, segments=16, rings=10)


def build_gentle_eyes(head, materials):
    """Relax the stare without changing the established A head silhouette."""
    for side in SIDES:
        eye_x = side * .43
        detail = materials["eye_detail"]
        ellipsoid(f"Eye_{side}", (eye_x, -.494, 2.91), (.196, .128, .143), materials["eye"], head)
        # Centered, larger warm-brown pupils replace the sideways skeptical stare.
        pupil = ellipsoid(f"Pupil_{side}", (eye_x, -.617, 2.90), (.091, .028, .106), detail, head)
        trim_local_z(pupil, .67, keep_upper=False)
        ellipsoid(f"Catchlight_{side}", (eye_x - .027, -.644, 2.937),
                  (.022, .009, .024), materials["catchlight"], head)
        # A thin cap follows the eyeball surface: no protruding lid bulb and no
        # harsh eyeliner. Trim geometry rather than flattening all lower vertices.
        lid = ellipsoid(f"RelaxedLid_{side}", (eye_x, -.494, 2.91),
                        (.203, .135, .148), materials["orange"], head)
        trim_local_z(lid, .42, keep_upper=True)
        # Soft horizontal arches, not sharp V-shaped angry inner eyebrows.
        brow_lift = .015 if side > 0 else 0
        tapered_stroke(f"Brow_{side}", [(side * .25, -.46, 3.16 + brow_lift),
                       (eye_x, -.42, 3.205 + brow_lift), (side * .61, -.34, 3.17 + brow_lift)],
                       [.3, 1, .3], .027, detail, head)


def build_face(head, materials):
    orange, cream = materials["orange"], materials["cream"]
    rounded_mass("Head", (0, .015, 2.65), (.77, .56, .73), orange, head, .86)
    muzzle = rounded_mass("SculptedMuzzle", (0, -.58, 2.32), (.82, .45, .46), cream, head, .78)
    for side in SIDES:
        carve_ellipsoid(muzzle, f"NostrilCut_{side}", (side * .43, -.973, 2.46),
                        (.166, .235, .192), (0, side * -.30, 0))
        # Dark backing is recessed well behind the carved opening.
        ellipsoid(f"NostrilInterior_{side}", (side * .43, -.792, 2.46),
                  (.125, .075, .15), materials["cavity"], head)
    build_gentle_eyes(head, materials)
    fuse_masses([muzzle], "SculptedMuzzle", head, voxel_size=.027)
    # One restrained off-center downturned mouth, not a friendly generic smile.
    tapered_stroke("SkepticalMouth", [(.22, -.995, 2.111), (.40, -.989, 2.12), (.59, -.93, 2.073)],
                   [.06, 1, .06], .020, materials["cavity"], head)


def build_head_silhouette(head, materials):
    orange, cream, dark = materials["orange"], materials["cream"], materials["charcoal"]
    for side in SIDES:
        height = .05 if side < 0 else 0
        tapered_stroke(f"Horn_{side}", [(side * .51, .02, 3.13), (side * .89, .025, 3.17),
                       (side * 1.08, .035, 3.34), (side * 1.06, .02, 3.57 + height)],
                       [1, .86, .55, .015], .19, cream, head, sides=3)
    ears = [((- .52, .10, 3.21), (-.76, .09, 3.91), .22),
            ((.66, .11, 3.01), (1.16, .02, 2.68), .23)]
    for index, (start, end, width) in enumerate(ears):
        leaf(f"EarOuter_{index}", start, end, width, .10, orange, head)
        inner_start = (start[0], start[1] - .075, start[2])
        inner_end = (end[0] * .97, end[1] - .085, end[2])
        leaf(f"EarInner_{index}", inner_start, inner_end, width * .65, .027, materials["ear_inner"], head)
    # Broad tapered forelock blades read as one crooked tuft at 112px, not beads.
    for index, offset in enumerate((-.16, 0, .16)):
        tapered_stroke(f"Forelock_{index}", [(.28 + offset, -.02, 3.29),
                       (.10 + offset, -.28, 3.47), (-.21 + offset, -.46, 3.30),
                       (-.38 + offset, -.47, 3.12)], [1, 1.05, .75, .015], .135, dark, head)
    for index, z in enumerate((3.12, 2.83, 2.54)):
        tapered_stroke(f"Mane_{index}", [(0, .47, z + .17), (0, .65, z), (0, .61, z - .26)],
                       [1, .9, .02], .17, dark, head)


def build_tail(tail, materials):
    tapered_stroke("Tail", [(.27, .49, 1.1), (.61, .60, .99), (.84, .50, .76)],
                   [1, .9, .55], .085, materials["orange"], tail)
    for index, offset in enumerate((-.10, 0, .10)):
        tapered_stroke(f"TailTuft_{index}", [(.81 + offset, .51, .83), (.92 + offset, .47, .65),
                       (1.0 + offset, .39, .50)], [1, .9, .015], .12, materials["charcoal"], tail)


def main():
    reset_scene()
    materials = {name: make_material(name, color, roughness=.72) for name, color in COLORS.items()}
    root = empty(ROOT_NAME)
    head, tail = empty("HeadRig", root), empty("TailRig", root)
    arms = [empty("LeftArmRig", root), empty("RightArmRig", root)]
    build_body(root, arms, materials)
    build_face(head, materials)
    build_head_silhouette(head, materials)
    build_tail(tail, materials)
    for joint, origin in [(head, (0, .02, 2.10)), (tail, (.27, .49, 1.1)),
                          (arms[0], (-.52, .04, 1.93)), (arms[1], (.52, .04, 1.93))]:
        localize_joint(joint, origin)
    export_glb(root, OUTPUT_MODEL)
    render_poster(OUTPUT_POSTER, look_at=(0, 0, 1.99), camera=(2.1, -9, 3.2), ortho_scale=4.35)
    # Optional review output is ignored and never shipped instead of a real model.
    if "--preview" in sys.argv:
        PREVIEW_DIRECTORY.mkdir(parents=True, exist_ok=True)
        scene = bpy.context.scene
        scene.render.resolution_x = scene.render.resolution_y = 1000
        scene.render.filepath = str(PREVIEW_DIRECTORY / "niuma-a-model.png")
        bpy.ops.render.render(write_still=True)


if __name__ == "__main__":
    main()
