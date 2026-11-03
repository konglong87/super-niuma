"""Small reusable sculpting primitives for local Blender companion generators."""

import math

import bpy
import bmesh
from mathutils import Vector

from pet_pipeline import ellipsoid, tube


def empty(name, parent=None):
    obj = bpy.data.objects.new(name, None)
    bpy.context.collection.objects.link(obj)
    obj.parent = parent
    return obj


def activate(obj):
    bpy.ops.object.select_all(action="DESELECT")
    obj.select_set(True)
    bpy.context.view_layer.objects.active = obj


def rounded_mass(name, position, scale, material, parent, exponent=0.8,
                 segments=28, rings=18):
    """A gently squared superellipsoid, with welded poles and outward normals."""
    def signed_power(value):
        return math.copysign(abs(value) ** exponent, value)

    vertices = [(0, 0, -scale[2])]
    for ring in range(1, rings):
        latitude = -math.pi / 2 + math.pi * ring / rings
        for segment in range(segments):
            longitude = math.tau * segment / segments
            vertices.append((
                scale[0] * signed_power(math.cos(latitude)) * signed_power(math.cos(longitude)),
                scale[1] * signed_power(math.cos(latitude)) * signed_power(math.sin(longitude)),
                scale[2] * signed_power(math.sin(latitude)),
            ))
    top = len(vertices)
    vertices.append((0, 0, scale[2]))
    faces = []
    for segment in range(segments):
        nxt = (segment + 1) % segments
        faces.append((0, 1 + nxt, 1 + segment))
        for ring in range(rings - 2):
            a = 1 + ring * segments
            b = a + segments
            faces.append((a + segment, a + nxt, b + nxt, b + segment))
        last = 1 + (rings - 2) * segments
        faces.append((last + segment, last + nxt, top))
    mesh = bpy.data.meshes.new(name)
    mesh.from_pydata(vertices, [], faces)
    mesh.update()
    obj = bpy.data.objects.new(name, mesh)
    bpy.context.collection.objects.link(obj)
    obj.location = position
    obj.parent = parent
    mesh.materials.append(material)
    for polygon in mesh.polygons:
        polygon.use_smooth = True
    return obj


def carve_ellipsoid(obj, name, position, scale, rotation=(0, 0, 0)):
    """Real concave geometry, not a painted black circle on the nose."""
    cutter = ellipsoid(name, position, scale, obj.data.materials[0])
    cutter.rotation_euler = rotation
    activate(obj)
    modifier = obj.modifiers.new(name, "BOOLEAN")
    modifier.operation = "DIFFERENCE"
    modifier.solver = "EXACT"
    modifier.object = cutter
    bpy.ops.object.modifier_apply(modifier=modifier.name)
    bpy.data.objects.remove(cutter, do_unlink=True)


def tapered_stroke(name, points, radii, radius, material, parent, sides=2):
    obj = tube(name, points, radii, radius, material, parent)
    obj.data.bevel_resolution = sides
    obj.data.resolution_u = 7
    obj.data.use_fill_caps = True
    # glTF contains meshes, not Blender-only curves. Convert before export so
    # horns, eyebrows and forelock survive outside the Blender poster render.
    activate(obj)
    bpy.ops.object.convert(target="MESH")
    return bpy.context.object


def leaf(name, start, end, width, depth, material, parent):
    """Curved pointed leaf for an ear; longitudinal profile avoids capsule tips."""
    obj = ellipsoid(name, (0, 0, 0), (1, 1, 1), material, parent)
    direction = Vector(end) - Vector(start)
    for vertex in obj.data.vertices:
        x, y, z = vertex.co
        t = (z + 1) / 2
        vertex.co = (x * width * (1.15 - .5 * t),
                     y * depth - .08 * math.sin(math.pi * t),
                     t * direction.length)
    obj.rotation_euler = direction.to_track_quat("Z", "Y").to_euler()
    obj.location = start
    return obj


def localize_joint(joint, origin):
    """Author in world coordinates, then pivot at the anatomical attachment."""
    for child in joint.children:
        child.location -= Vector(origin)
    joint.location = origin


def fuse_masses(objects, name, parent, voxel_size=.045):
    """Fuse overlapping body volumes; smooth and simplify for a mobile-size mesh."""
    activate(objects[0])
    for obj in objects[1:]:
        obj.select_set(True)
    if len(objects) > 1:
        bpy.ops.object.join()
    body = bpy.context.object
    body.name = name
    bpy.ops.object.transform_apply(location=False, rotation=False, scale=True)
    remesh = body.modifiers.new("SeamlessVolume", "REMESH")
    remesh.mode = "VOXEL"
    remesh.voxel_size = voxel_size
    bpy.ops.object.modifier_apply(modifier=remesh.name)
    smooth = body.modifiers.new("SculptRelax", "SMOOTH")
    smooth.factor = 1.1
    smooth.iterations = 5
    bpy.ops.object.modifier_apply(modifier=smooth.name)
    decimate = body.modifiers.new("DesktopBudget", "DECIMATE")
    decimate.ratio = .32
    bpy.ops.object.modifier_apply(modifier=decimate.name)
    for face in body.data.polygons:
        face.use_smooth = True
    body.parent = parent
    return body


def trim_local_z(obj, height, keep_upper):
    """Trim a surface in mesh-local coordinates (hidden eye/lid boundaries)."""
    mesh = bmesh.new()
    mesh.from_mesh(obj.data)
    bmesh.ops.bisect_plane(mesh, geom=list(mesh.verts) + list(mesh.edges) + list(mesh.faces),
                          plane_co=(0, 0, height), plane_no=(0, 0, 1),
                          clear_inner=keep_upper, clear_outer=not keep_upper)
    mesh.to_mesh(obj.data)
    mesh.free()
