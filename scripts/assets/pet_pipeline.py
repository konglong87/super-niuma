"""Shared Blender helpers for reproducible niuma pet assets.

Imported by the per-pet build scripts (run with `blender --background --python`).
Each pet exports a GLB plus a transparent poster rendered from the same scene.
"""

from pathlib import Path

import bpy
from mathutils import Vector

REPO_ROOT = Path(__file__).resolve().parents[2]
PET_OUTPUT = REPO_ROOT / "web" / "public" / "assets" / "pets"


def reset_scene():
    bpy.ops.object.select_all(action="SELECT")
    bpy.ops.object.delete(use_global=False)
    PET_OUTPUT.mkdir(parents=True, exist_ok=True)


def make_material(name, color, metallic=0.0, roughness=0.32):
    mat = bpy.data.materials.new(name)
    mat.diffuse_color = (*color, 1)
    mat.use_nodes = True
    shader = mat.node_tree.nodes.get("Principled BSDF")
    shader.inputs["Base Color"].default_value = (*color, 1)
    shader.inputs["Metallic"].default_value = metallic
    shader.inputs["Roughness"].default_value = roughness
    return mat


def _smooth(obj):
    if hasattr(obj.data, "polygons"):
        for poly in obj.data.polygons:
            poly.use_smooth = True
    return obj


def ellipsoid(name, position, scale, mat, parent=None):
    bpy.ops.mesh.primitive_uv_sphere_add(segments=24, ring_count=16, location=position)
    obj = bpy.context.object
    obj.name = name
    obj.scale = scale
    obj.data.materials.append(mat)
    if parent is not None:
        obj.parent = parent
    return _smooth(obj)


def cone(name, position, radius, depth, mat, rotation=(0, 0, 0), parent=None, vertices=20):
    bpy.ops.mesh.primitive_cone_add(
        vertices=vertices, radius1=radius, radius2=0.0, depth=depth,
        location=position, rotation=rotation,
    )
    obj = bpy.context.object
    obj.name = name
    obj.data.materials.append(mat)
    if parent is not None:
        obj.parent = parent
    return _smooth(obj)


def tube(name, points, radii, base_radius, mat, parent=None):
    """Tapered tube along a smooth bezier path; radii multiply base_radius."""
    curve = bpy.data.curves.new(name, "CURVE")
    curve.dimensions = "3D"
    curve.bevel_depth = base_radius
    curve.bevel_resolution = 3
    curve.resolution_u = 12
    spline = curve.splines.new("BEZIER")
    spline.bezier_points.add(len(points) - 1)
    for bp, co, radius in zip(spline.bezier_points, points, radii):
        bp.co = co
        bp.radius = radius
        bp.handle_left_type = "AUTO"
        bp.handle_right_type = "AUTO"
    curve.materials.append(mat)
    obj = bpy.data.objects.new(name, curve)
    bpy.context.collection.objects.link(obj)
    if parent is not None:
        obj.parent = parent
    return obj


def export_glb(root, filename):
    bpy.ops.object.select_all(action="DESELECT")
    root.select_set(True)
    for child in root.children_recursive:
        child.select_set(True)
    bpy.context.view_layer.objects.active = root
    bpy.ops.export_scene.gltf(
        filepath=str(PET_OUTPUT / filename),
        export_format="GLB", use_selection=True, export_animations=False,
    )


def render_poster(filename, look_at=(0, 0, 1.0), camera=(2.2, -6.5, 2.6), ortho_scale=2.5, resolution=(336, 396)):
    scene = bpy.context.scene
    scene.render.engine = "CYCLES"
    scene.cycles.samples = 24
    scene.render.resolution_x, scene.render.resolution_y = resolution
    scene.render.resolution_percentage = 100
    scene.render.film_transparent = True
    scene.world.color = (0.3, 0.3, 0.3)
    scene.view_settings.view_transform = "Standard"
    bpy.ops.object.camera_add(location=camera)
    cam = bpy.context.object
    cam.rotation_euler = (Vector(look_at) - cam.location).to_track_quat("-Z", "Y").to_euler()
    cam.data.type = "ORTHO"
    cam.data.ortho_scale = ortho_scale
    scene.camera = cam
    for position, energy, size in [((2, -4, 6), 450, 5), ((-3, -2, 3), 200, 4), ((0, 3, 4), 350, 3)]:
        bpy.ops.object.light_add(type="AREA", location=position)
        light = bpy.context.object
        light.data.energy = energy
        light.data.shape = "DISK"
        light.data.size = size
        light.rotation_euler = (Vector((0, 0, 1)) - light.location).to_track_quat("-Z", "Y").to_euler()
    scene.render.filepath = str(PET_OUTPUT / filename)
    bpy.ops.render.render(write_still=True)
