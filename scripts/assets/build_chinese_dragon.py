"""Build the close-up, soft-clay Chinese baby dragon desktop pet."""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import bpy  # noqa: E402
from mathutils import Euler  # noqa: E402
from pet_pipeline import (  # noqa: E402
    cone,
    ellipsoid,
    export_glb,
    make_material,
    render_poster,
    reset_scene,
    tube,
)


reset_scene()

# A soft-clay palette: cyan body planes, pearl scales, lavender markings,
# and restrained coral accents reserved for cheeks, tongue, and nostrils.
body = make_material("Azure clay", (0.10, 0.51, 0.76), 0.01, 0.34)
body_shadow = make_material("Azure shadow", (0.045, 0.25, 0.47), 0.02, 0.36)
body_mid = make_material("Azure midtone", (0.16, 0.64, 0.84), 0.0, 0.31)
body_light = make_material("Azure highlight", (0.42, 0.82, 0.94), 0.0, 0.28)
body_deep = make_material("Azure deep detail", (0.025, 0.16, 0.32), 0.02, 0.29)
mane_blue = make_material("Mane blue", (0.20, 0.70, 0.89), 0.0, 0.28)
mane_lavender = make_material("Mane lavender", (0.53, 0.53, 0.80), 0.0, 0.31)
mane_lavender_light = make_material("Mane lavender light", (0.74, 0.73, 0.94), 0.0, 0.29)
pearl = make_material("Pearl scales", (0.94, 0.95, 0.88), 0.0, 0.35)
pearl_shadow = make_material("Pearl scale shadow", (0.73, 0.84, 0.87), 0.0, 0.36)
cheek_blush = make_material("Soft coral blush", (1.0, 0.43, 0.50), 0.0, 0.34)
nostril = make_material("Coral nostril", (0.92, 0.22, 0.28), 0.0, 0.27)
mouth = make_material("Warm mouth", (0.23, 0.025, 0.07), 0.0, 0.30)
tongue = make_material("Warm tongue", (1.0, 0.28, 0.40), 0.0, 0.27)
eye_rim = make_material("Turquoise eye rim", (0.055, 0.34, 0.64), 0.01, 0.20)
eye_white = make_material("Ice eye white", (0.76, 0.91, 0.94), 0.0, 0.23)
eye_iris = make_material("Midnight iris", (0.005, 0.018, 0.052), 0.03, 0.10)
eye_glint = make_material("Eye glints", (1.0, 1.0, 0.98), 0.0, 0.08)
horn = make_material("Ivory horns", (0.91, 0.91, 0.82), 0.02, 0.31)
horn_shadow = make_material("Ivory horn shadow", (0.68, 0.75, 0.73), 0.01, 0.35)
tooth = make_material("Tiny pearl fangs", (0.96, 0.95, 0.83), 0.0, 0.30)

root = bpy.data.objects.new("ChineseDragon", None)
bpy.context.collection.objects.link(root)


def rig(name, location, rotation=(0.0, 0.0, 0.0), parent=None):
    obj = bpy.data.objects.new(name, None)
    bpy.context.collection.objects.link(obj)
    obj.location = location
    obj.rotation_euler = Euler(rotation, "XYZ")
    if parent is not None:
        obj.parent = parent
    return obj


def local_ellipsoid(name, position, scale, material, parent, rotation=(0.0, 0.0, 0.0)):
    obj = ellipsoid(name, position, scale, material, parent)
    obj.rotation_euler = Euler(rotation, "XYZ")
    return obj


def local_tube(name, points, radii, base_radius, material, parent):
    return tube(name, points, radii, base_radius, material, parent)


def local_cone(name, position, radius, depth, material, parent, rotation=(0.0, 0.0, 0.0)):
    return cone(name, position, radius, depth, material, rotation, parent)


def scale_plate(name, position, scale, material, parent, rotation=(0.0, 0.0, 0.0)):
    return local_ellipsoid(name, position, scale, material, parent, rotation)


body_rig = rig("BodyRig", (0.0, 0.03, 0.79), parent=root)
head_rig = rig("HeadRig", (0.035, -0.015, 1.69), rotation=(0.0, 0.0, -0.20), parent=root)
tail_rig = rig("TailRig", (-0.08, 0.19, 0.36), rotation=(0.0, 0.0, 0.05), parent=root)
left_arm_rig = rig("LeftArmRig", (-0.39, -0.10, 1.00), rotation=(0.0, 0.0, -0.10), parent=root)
right_arm_rig = rig("RightArmRig", (0.39, -0.10, 1.00), rotation=(0.0, 0.0, 0.08), parent=root)

# Compact pear-shaped body. The shorter torso stops the silhouette reading as
# a seated rabbit and leaves the head as the clear visual hero.
local_ellipsoid("Torso", (0.0, 0.05, 0.0), (0.54, 0.42, 0.59), body, body_rig)
local_ellipsoid("ShoulderMantle", (0.0, 0.07, 0.30), (0.46, 0.36, 0.30), body_shadow, body_rig)
local_ellipsoid("ChestPlane", (0.0, -0.36, 0.15), (0.34, 0.10, 0.34), body_mid, body_rig)

# Overlapping pearl scales form a tapered belly instead of a stack of white
# buttons. The side scales make the plate pattern survive a small render.
belly_rows = [
    (-0.48, 0.22, 0.28, 0.16),
    (-0.48, 0.39, 0.26, 0.15),
    (-0.45, 0.55, 0.24, 0.14),
    (-0.41, 0.70, 0.22, 0.13),
    (-0.36, 0.84, 0.19, 0.12),
    (-0.30, 0.96, 0.15, 0.10),
]
for index, (y, z, width, height) in enumerate(belly_rows):
    scale_plate(
        f"BellyScale_{index}",
        (0.0, y, z - 0.79),
        (width, 0.082, height),
        pearl if index % 2 == 0 else pearl_shadow,
        body_rig,
        rotation=(0.08, 0.0, 0.0),
    )
    if index < 5:
        for side in (-1, 1):
            scale_plate(
                f"BellySideScale_{index}_{side}",
                (side * width * 0.71, y + 0.015, z - 0.79),
                (width * 0.40, 0.058, height * 0.70),
                pearl_shadow,
                body_rig,
                rotation=(0.10, side * 0.12, side * 0.09),
            )

# Haunches tuck under the body; the feet are turned inward rather than laid
# flat like a mascot's shoes.
for side in (-1, 1):
    local_ellipsoid(f"Haunch_{side}", (side * 0.36, 0.05, -0.28), (0.30, 0.31, 0.27), body, body_rig)
    local_ellipsoid(
        f"Foot_{side}",
        (side * 0.34, -0.31, -0.48),
        (0.25, 0.25, 0.15),
        body_mid,
        body_rig,
        rotation=(0.0, side * 0.10, side * 0.08),
    )
    for toe in (-1, 0, 1):
        local_ellipsoid(
            f"FootClaw_{side}_{toe}",
            (side * 0.34 + toe * 0.070, -0.52, -0.49),
            (0.040, 0.026, 0.048),
            tooth,
            body_rig,
        )

# A thick tail sits behind the body and curls into a readable crescent.
tail_points = [
    (0.08, 0.10, 0.10),
    (-0.30, 0.17, -0.02),
    (-0.67, 0.18, -0.22),
    (-0.84, 0.10, -0.43),
    (-0.71, -0.04, -0.56),
    (-0.38, -0.10, -0.52),
]
local_tube("Tail", tail_points, [1.0, 1.12, 1.04, 0.82, 0.54, 0.12], 0.18, body_shadow, tail_rig)
for index, (position, scale, material) in enumerate(
    (
        ((-0.80, 0.10, -0.34), (0.16, 0.13, 0.15), body_mid),
        ((-0.87, 0.06, -0.48), (0.13, 0.11, 0.16), body_light),
        ((-0.76, -0.02, -0.61), (0.14, 0.10, 0.11), mane_lavender),
        ((-0.56, -0.09, -0.65), (0.13, 0.09, 0.10), body_light),
    )
):
    local_ellipsoid(f"TailTuft_{index}", position, scale, material, tail_rig)
for index, (x, z, rotation) in enumerate(
    ((-0.57, -0.18, -0.35), (-0.72, -0.34, -0.52), (-0.78, -0.47, -0.70))
):
    local_cone(
        f"TailFin_{index}",
        (x, 0.04, z),
        0.09 - index * 0.012,
        0.22 - index * 0.02,
        mane_blue,
        tail_rig,
        rotation=(0.0, rotation, -0.25),
    )

# Short folded arms rest against the chest. One arm is raised just enough to
# create an asymmetrical, lively pose.
for side, arm_parent, lift in ((-1, left_arm_rig, 0.08), (1, right_arm_rig, 0.0)):
    local_ellipsoid("UpperArm", (0.0, 0.01, -0.03 + lift), (0.16, 0.18, 0.27), body, arm_parent)
    local_ellipsoid(
        "Forearm",
        (-side * 0.10, -0.19, -0.18 + lift),
        (0.15, 0.16, 0.21),
        body_mid,
        arm_parent,
        rotation=(0.0, side * 0.15, side * 0.12),
    )
    local_ellipsoid(
        "Palm",
        (-side * 0.14, -0.33, -0.29 + lift),
        (0.14, 0.12, 0.13),
        body_light,
        arm_parent,
    )
    for index, z in enumerate((0.08, -0.03, -0.13)):
        scale_plate(
            f"ArmScale_{side}_{index}",
            (-side * 0.08, -0.21, z + lift),
            (0.11 - index * 0.012, 0.034, 0.065),
            body_light if index == 0 else body_mid,
            arm_parent,
            rotation=(0.05, side * 0.08, side * 0.12),
        )
    for finger in (-1, 0, 1):
        local_ellipsoid(
            f"FingerClaw_{side}_{finger}",
            (-side * 0.14 + finger * 0.045, -0.43, -0.31 + lift),
            (0.030, 0.022, 0.048),
            tooth,
            arm_parent,
        )

# ---------------------------------------------------------------------------
# Head and face
# ---------------------------------------------------------------------------

# The head is a single broad, rounded volume. A cyan face plane and lower jaw
# keep the muzzle integrated instead of producing two white mouse cheeks.
local_ellipsoid("HeadBase", (0.0, 0.02, 0.0), (0.76, 0.57, 0.63), body, head_rig)
local_ellipsoid("FacePlane", (0.0, -0.37, -0.01), (0.62, 0.18, 0.50), body_mid, head_rig)
local_ellipsoid("LowerJaw", (0.0, -0.49, -0.29), (0.36, 0.12, 0.13), pearl_shadow, head_rig)
local_ellipsoid("MuzzlePanel", (0.0, -0.56, -0.12), (0.43, 0.13, 0.16), body_light, head_rig)

# Eyes are large but shaped as almond-like rounded ovals, not black buttons.
for side in (-1, 1):
    eye_x = side * 0.285
    local_ellipsoid(f"EyeRim_{side}", (eye_x, -0.455, 0.15), (0.27, 0.070, 0.29), eye_rim, head_rig)
    local_ellipsoid(f"EyeWhite_{side}", (eye_x, -0.510, 0.15), (0.232, 0.050, 0.255), eye_white, head_rig)
    local_ellipsoid(f"Iris_{side}", (eye_x, -0.558, 0.14), (0.158, 0.038, 0.185), eye_iris, head_rig)
    local_ellipsoid(
        f"EyeGlintLarge_{side}",
        (eye_x + side * 0.042, -0.590, 0.225),
        (0.055, 0.015, 0.070),
        eye_glint,
        head_rig,
    )
    local_ellipsoid(
        f"EyeGlintSmall_{side}",
        (eye_x - side * 0.055, -0.592, 0.095),
        (0.020, 0.010, 0.028),
        eye_glint,
        head_rig,
    )
    local_tube(
        f"UpperLid_{side}",
        [
            (side * 0.48, -0.50, 0.31),
            (side * 0.30, -0.56, 0.42),
            (side * 0.09, -0.51, 0.34),
        ],
        [0.14, 0.78, 0.14],
        0.032,
        body_light,
        head_rig,
    )
    local_ellipsoid(
        f"Blush_{side}",
        (side * 0.46, -0.54, -0.08),
        (0.16, 0.028, 0.085),
        cheek_blush,
        head_rig,
    )

# Small blue nose, coral nostrils, and a simple smile. There are no white
# front teeth; only two tiny upper fangs peek out at the corners.
local_ellipsoid("NoseBridge", (0.0, -0.64, -0.01), (0.12, 0.065, 0.075), body_light, head_rig)
local_ellipsoid("Nose", (0.0, -0.705, -0.01), (0.070, 0.034, 0.048), body_mid, head_rig)
for side in (-1, 1):
    local_ellipsoid(
        f"Nostril_{side}",
        (side * 0.035, -0.738, 0.0),
        (0.014, 0.008, 0.010),
        nostril,
        head_rig,
    )

local_ellipsoid("OpenMouth", (0.0, -0.625, -0.30), (0.22, 0.028, 0.095), mouth, head_rig)
local_ellipsoid("Tongue", (0.025, -0.663, -0.33), (0.085, 0.018, 0.042), tongue, head_rig)
local_ellipsoid(
    "SingleCornerFang",
    (0.115, -0.655, -0.275),
    (0.011, 0.008, 0.026),
    tooth,
    head_rig,
    rotation=(0.0, 0.08, 0.05),
)

# The forehead is intentionally clean: a small scale patch and two subtle
# cloud curls replace the old protruding white bead row.
local_ellipsoid(
    "ForeheadGem",
    (0.0, -0.571, 0.44),
    (0.026, 0.008, 0.036),
    mane_lavender_light,
    head_rig,
    rotation=(0.08, 0.0, 0.0),
)
for side in (-1, 1):
    local_tube(
        f"ForeheadCloud_{side}",
        [
            (side * 0.055, -0.574, 0.48),
            (side * 0.15, -0.588, 0.57),
            (side * 0.25, -0.578, 0.50),
            (side * 0.20, -0.580, 0.42),
        ],
        [0.15, 0.84, 0.68, 0.10],
        0.024,
        mane_lavender,
        head_rig,
    )

# Layered locks frame the face with a more organic, reference-like silhouette.
for name, position, scale, material, angle in (
    ("CrownCenter", (0.0, 0.16, 0.58), (0.25, 0.20, 0.25), body_light, 0.0),
    ("CrownLeft", (-0.24, 0.14, 0.54), (0.22, 0.18, 0.30), mane_lavender_light, -0.24),
    ("CrownRight", (0.24, 0.14, 0.54), (0.22, 0.18, 0.30), mane_lavender_light, 0.24),
    ("CheekLeft", (-0.60, 0.08, 0.16), (0.18, 0.15, 0.28), body_light, -0.35),
    ("CheekRight", (0.60, 0.08, 0.16), (0.18, 0.15, 0.28), body_light, 0.35),
    ("JawLeft", (-0.57, 0.07, -0.18), (0.17, 0.14, 0.23), mane_lavender, -0.43),
    ("JawRight", (0.57, 0.07, -0.18), (0.17, 0.14, 0.23), mane_lavender, 0.43),
    ("NeckLeft", (-0.47, 0.10, -0.45), (0.15, 0.13, 0.22), body_light, -0.52),
    ("NeckRight", (0.47, 0.10, -0.45), (0.15, 0.13, 0.22), body_light, 0.52),
):
    local_ellipsoid(name, position, scale, material, head_rig, rotation=(0.0, angle * 0.22, angle))

for side in (-1, 1):
    for index, (x, z, length, material) in enumerate(
        (
            (0.39, 0.65, 0.30, body_light),
            (0.52, 0.48, 0.25, mane_lavender),
            (0.58, 0.25, 0.20, body_light),
        )
    ):
        base_x = side * x
        local_tube(
            f"ManeTuft_{side}_{index}",
            [
                (base_x, 0.12, z),
                (base_x + side * 0.10, 0.15, z + length * 0.52),
                (base_x + side * 0.16, 0.10, z + length),
            ],
            [1.0, 0.76, 0.10],
            0.072 if index == 0 else 0.056,
            material,
            head_rig,
        )

# Low-profile ear fins read as dragon anatomy instead of round cat ears.
for side in (-1, 1):
    local_ellipsoid(f"EarBase_{side}", (side * 0.65, 0.05, 0.38), (0.15, 0.10, 0.18), body_mid, head_rig)
    local_cone(
        f"EarFin_{side}",
        (side * 0.72, 0.08, 0.56),
        0.115,
        0.34,
        body_light,
        head_rig,
        rotation=(0.0, side * 0.52, side * 0.08),
    )
    local_cone(
        f"EarFinInner_{side}",
        (side * 0.73, 0.00, 0.56),
        0.058,
        0.22,
        mane_lavender,
        head_rig,
        rotation=(0.0, side * 0.52, side * 0.08),
    )

# Thick curved horns sit behind the forehead, with a soft ivory shadow plane.
for side in (-1, 1):
    local_tube(
        f"Horn_{side}",
        [
            (side * 0.27, 0.16, 0.43),
            (side * 0.45, 0.18, 0.57),
            (side * 0.59, 0.19, 0.76),
            (side * 0.62, 0.18, 0.94 if side < 0 else 1.02),
            (side * 0.52, 0.18, 1.05 if side < 0 else 1.14),
        ],
        [1.0, 1.0, 0.82, 0.50, 0.10],
        0.070,
        horn,
        head_rig,
    )
    local_tube(
        f"HornShadow_{side}",
        [
            (side * 0.31, 0.105, 0.50),
            (side * 0.49, 0.12, 0.68),
            (side * 0.57, 0.13, 0.88),
        ],
        [0.85, 0.66, 0.12],
        0.022,
        horn_shadow,
        head_rig,
    )

# Small lavender cloud curls sit behind the cheeks and connect the head to the
# mane without creating another focal point on the face.
for side in (-1, 1):
    local_tube(
        f"CheekCloud_{side}",
        [
            (side * 0.54, 0.10, -0.01),
            (side * 0.70, 0.11, 0.10),
            (side * 0.71, 0.10, 0.27),
            (side * 0.59, 0.10, 0.34),
        ],
        [0.72, 0.92, 0.58, 0.10],
        0.030,
        mane_lavender_light,
        head_rig,
    )

export_glb(root, "chinese-dragon.glb")
render_poster(
    "chinese-dragon.png",
    look_at=(0.0, 0.0, 1.27),
    camera=(2.35, -7.4, 2.82),
    ortho_scale=3.12,
)
