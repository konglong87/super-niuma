"""Rebuild the original niuma companion using Blender in background mode."""

from pathlib import Path
import bpy
from mathutils import Vector

ROOT = Path(__file__).resolve().parents[2]
OUTPUT = ROOT / "web" / "public" / "assets" / "pets"
OUTPUT.mkdir(parents=True, exist_ok=True)
bpy.ops.object.select_all(action="SELECT")
bpy.ops.object.delete(use_global=False)


def material(name, color, metallic=0.0):
    mat = bpy.data.materials.new(name)
    mat.diffuse_color = (*color, 1)
    mat.use_nodes = True
    shader = mat.node_tree.nodes.get("Principled BSDF")
    shader.inputs["Base Color"].default_value = (*color, 1)
    shader.inputs["Metallic"].default_value = metallic
    shader.inputs["Roughness"].default_value = 0.32
    return mat


teal = material("Jade enamel", (0.035, 0.58, 0.46), 0.2)
dark = material("Graphite", (0.022, 0.035, 0.043), 0.25)
white = material("Porcelain", (0.91, 0.97, 0.98))
amber = material("Signal amber", (1.0, 0.48, 0.085), 0.15)
root = bpy.data.objects.new("GoCompanion", None)
bpy.context.collection.objects.link(root)


def ellipsoid(name, position, scale, mat):
    bpy.ops.mesh.primitive_uv_sphere_add(segments=24, ring_count=16, location=position)
    obj = bpy.context.object
    obj.name = name
    obj.scale = scale
    obj.data.materials.append(mat)
    obj.parent = root
    for poly in obj.data.polygons:
        poly.use_smooth = True
    return obj


ellipsoid("Body", (0, 0, 0.95), (0.64, 0.43, 0.73), teal)
ellipsoid("Face", (0, -0.36, 1.12), (0.5, 0.13, 0.37), white)
for side in (-1, 1):
    ellipsoid("Eye", (side * 0.2, -0.477, 1.18), (0.074, 0.05, 0.105), dark)
    ellipsoid("Eye glint", (side * 0.2 - 0.015, -0.52, 1.21), (0.018, 0.012, 0.026), white)
    ellipsoid("Foot", (side * 0.31, -0.075, 0.18), (0.25, 0.33, 0.16), dark)
    arm = ellipsoid("Arm", (side * 0.68, -0.015, 0.88), (0.15, 0.17, 0.32), teal)
    arm.rotation_euler.y = side * -0.35
    ellipsoid("Ear", (side * 0.52, 0, 1.55), (0.16, 0.17, 0.21), teal)
ellipsoid("Smile", (0, -0.485, 0.96), (0.10, 0.018, 0.035), dark)
ellipsoid("Antenna", (0, 0, 1.78), (0.055, 0.055, 0.18), dark)
ellipsoid("Beacon", (0, 0, 1.94), (0.105, 0.105, 0.105), amber)
ellipsoid("Badge", (0, -0.417, 0.64), (0.11, 0.023, 0.075), amber)
bpy.ops.object.select_all(action="DESELECT")
root.select_set(True)
for obj in root.children:
    obj.select_set(True)
bpy.context.view_layer.objects.active = root
bpy.ops.export_scene.gltf(
    filepath=str(OUTPUT / "go-companion.glb"),
    export_format="GLB", use_selection=True, export_animations=False,
)

scene = bpy.context.scene
scene.render.engine = "CYCLES"
scene.cycles.samples = 24
scene.render.resolution_x = 336
scene.render.resolution_y = 396
scene.render.resolution_percentage = 100
scene.render.film_transparent = True
scene.world.color = (0.3, 0.3, 0.3)
scene.view_settings.view_transform = "Standard"
bpy.ops.object.camera_add(location=(2.5, -6, 2.7))
camera = bpy.context.object
camera.rotation_euler = (Vector((0, 0, 1.0)) - camera.location).to_track_quat("-Z", "Y").to_euler()
camera.data.type = "ORTHO"
camera.data.ortho_scale = 2.65
scene.camera = camera
for position, energy, size in [((2, -4, 6), 450, 5), ((-3, -2, 3), 200, 4), ((0, 3, 4), 350, 3)]:
    bpy.ops.object.light_add(type="AREA", location=position)
    light = bpy.context.object
    light.data.energy = energy
    light.data.shape = "DISK"
    light.data.size = size
    light.rotation_euler = (Vector((0, 0, 1)) - light.location).to_track_quat("-Z", "Y").to_euler()
scene.render.filepath = str(OUTPUT / "go-companion.png")
bpy.ops.render.render(write_still=True)
