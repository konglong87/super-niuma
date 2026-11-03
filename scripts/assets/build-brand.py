"""Render two semantic endpoints walking together. Blender, then ffmpeg."""

from pathlib import Path
from tempfile import TemporaryDirectory
import math
import subprocess
import bpy
from mathutils import Vector

ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "docs" / "web_agent" / "images"
OUTPUT.mkdir(parents=True, exist_ok=True)
FRAMES = 48
bpy.ops.object.select_all(action="SELECT")
bpy.ops.object.delete(use_global=False)


def material(name, color):
    mat = bpy.data.materials.new(name)
    mat.diffuse_color = (*color, 1)
    mat.use_nodes = True
    shader = mat.node_tree.nodes.get("Principled BSDF")
    shader.inputs["Base Color"].default_value = (*color, 1)
    shader.inputs["Roughness"].default_value = 0.35
    return mat


teal = material("Intent", (0.035, 0.48, 0.37))
amber = material("Evidence", (0.96, 0.43, 0.055))
ink = material("Ink", (0.022, 0.035, 0.043))
paper = material("Paper", (0.95, 0.97, 0.98))


def cube(name, position, size, mat):
    bpy.ops.mesh.primitive_cube_add(size=1, location=position)
    obj = bpy.context.object
    obj.name = name
    obj.scale = size
    bpy.ops.object.transform_apply(location=False, rotation=False, scale=True)
    bevel = obj.modifiers.new("Soft edges", "BEVEL")
    bevel.width = 0.10
    bevel.segments = 4
    obj.modifiers.new("Normals", "WEIGHTED_NORMAL")
    obj.data.materials.append(mat)
    return obj


def text(value, position, size, mat):
    curve = bpy.data.curves.new(value, "FONT")
    curve.body = value
    curve.align_x = "CENTER"
    curve.align_y = "CENTER"
    curve.size = size
    curve.extrude = 0.012
    obj = bpy.data.objects.new(value, curve)
    bpy.context.collection.objects.link(obj)
    obj.location = position
    obj.rotation_euler.x = math.pi / 2
    obj.data.materials.append(mat)
    return obj


endpoints = []
for side, mat in [(-1, teal), (1, amber)]:
    root = bpy.data.objects.new("Endpoint", None)
    bpy.context.collection.objects.link(root)
    body = cube("End", (0, 0, 1.15), (0.9, 0.65, 0.92), mat)
    body.parent = root
    label = text("e", (0, -0.335, 1.17), 0.8, paper)
    label.parent = root
    feet = []
    for x in (-0.22, 0.22):
        foot = cube("Step", (x, -0.05, 0.53), (0.28, 0.45, 0.2), ink)
        foot.parent = root
        feet.append(foot)
    endpoints.append((side, root, feet))
text("Go", (0, 0.02, 1.2), 0.72, ink)
text("niuma", (0, -0.10, -0.18), 0.58, ink)
text("End to end. Go together.", (0, -0.10, -0.78), 0.19, ink)
bridge = cube("Shared outcome", (0, 0.05, 0.48), (1.65, 0.22, 0.08), teal)

scene = bpy.context.scene
scene.render.engine = "CYCLES"
scene.cycles.samples = 12
scene.cycles.use_denoising = True
scene.render.resolution_x = 720
scene.render.resolution_y = 360
scene.render.resolution_percentage = 100
scene.render.image_settings.file_format = "PNG"
scene.world.use_nodes = True
scene.world.node_tree.nodes["Background"].inputs["Color"].default_value = (0.95, 0.97, 0.98, 1)
scene.world.node_tree.nodes["Background"].inputs["Strength"].default_value = 0.8
scene.view_settings.view_transform = "Standard"
bpy.ops.object.camera_add(location=(0, -10, 4.0))
camera = bpy.context.object
camera.rotation_euler = (Vector((0, 0, 0.55)) - camera.location).to_track_quat("-Z", "Y").to_euler()
camera.data.type = "ORTHO"
camera.data.ortho_scale = 7.2
scene.camera = camera
bpy.ops.object.light_add(type="AREA", location=(-3, -4, 6))
light = bpy.context.object
light.data.energy = 350
light.data.size = 6

with TemporaryDirectory(prefix="niuma-brand-") as directory:
    for index in range(FRAMES):
        # Walk in, hold the shared outcome, then separate for a seamless loop.
        progress = min(index / 20, 1) if index < 34 else (FRAMES - index) / 14
        progress = progress * progress * (3 - 2 * progress)
        for side, root, feet in endpoints:
            root.location.x = side * (2.45 - 1.40 * progress)
            walking = index < 20 or index >= 34
            root.location.z = abs(math.sin(index * 0.7)) * 0.08 if walking else 0
            for i, foot in enumerate(feet):
                foot.rotation_euler.x = math.sin(index * 0.7 + i * math.pi) * 0.35 if walking else 0
        bridge.scale.x = max(0.001, progress)
        scene.render.filepath = str(Path(directory) / f"{index:03d}.png")
        bpy.ops.render.render(write_still=True)
    subprocess.run([
        "ffmpeg", "-y", "-framerate", "12", "-i", str(Path(directory) / "%03d.png"),
        "-filter_complex", "[0:v]split[a][b];[a]palettegen[p];[b][p]paletteuse",
        "-loop", "0", str(OUTPUT / "niuma-together.gif"),
    ], check=True)
