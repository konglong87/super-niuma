import Foundation
import CoreGraphics

let maxDragSteps = 64

struct DisplayGeometry: Equatable {
    let id: String
    let bounds: CGRect // global CG event coordinates (points)
    let width: Int
    let height: Int
    let windowID: String?

    init(id: String, bounds: CGRect, width: Int, height: Int, windowID: String? = nil) {
        self.id = id; self.bounds = bounds; self.width = width; self.height = height; self.windowID = windowID
    }
    var scale: Double { Double(width) / bounds.width }
    var valid: Bool {
        bounds.minX.isFinite && bounds.minY.isFinite && bounds.width.isFinite && bounds.height.isFinite &&
        width > 0 && height > 0 && width <= maxImageDimension && height <= maxImageDimension &&
        width * height <= maxImagePixels && bounds.width > 0 && bounds.height > 0 && scale.isFinite && scale > 0 && scale <= 8 &&
        abs(scale - Double(height) / bounds.height) < 0.01
    }
    func point(x: Int, y: Int) throws -> CGPoint {
        guard valid, x >= 0, y >= 0, x < width, y < height else { throw SafetyError.invalidAction }
        return CGPoint(x: bounds.minX + Double(x) / scale, y: bounds.minY + Double(y) / scale)
    }
    var coordinateSpace: JSONValue {
        .object(["display_id": .string(id), "origin": .string("top_left"), "unit": .string("pixels"),
                 "width": .number(Double(width)), "height": .number(Double(height)), "scale_factor": .number(scale),
                 "bounds": .object(["x": .number(bounds.origin.x), "y": .number(bounds.origin.y),
                                    "width": .number(bounds.width), "height": .number(bounds.height)])])
    }
}

struct NativeWindow: Equatable {
    let id: String
    let title: String
    let ownerPID: Int32
    let bundleID: String
    let frame: CGRect
    let displayID: String
    let isVisible: Bool
    let isFrontmost: Bool

    func identityDiffs(_ other: NativeWindow) -> [String] {
        var differences: [String] = []
        if id != other.id { differences.append("window_id") }
        if ownerPID != other.ownerPID { differences.append("owner_pid") }
        if bundleID != other.bundleID { differences.append("bundle_id") }
        return differences
    }

    func matchesIdentity(_ other: NativeWindow) -> Bool {
        // Identity is the immutable Window Server identity: id, owner PID, and
        // bundle ID. Frame, displayID, and visibility can shift by a few pixels
        // or briefly toggle during launch animation/retina scaling, so they
        // must not be exact-match identity gates — a content-rendered window
        // that resized by 1px after launch would otherwise be rejected as a
        // different target. Frame is still validated for non-zero dimensions
        // by the caller.
        identityDiffs(other).isEmpty
    }

    var diagnosticFields: [String: Any] {
        ["id": id, "owner_pid": ownerPID, "bundle_id": bundleID, "display_id": displayID,
         "is_visible": isVisible, "is_frontmost": isFrontmost,
         "frame": ["x": frame.origin.x, "y": frame.origin.y, "width": frame.width, "height": frame.height]]
    }

    var json: JSONValue {
        .object([
            "id": .string(id), "title": .string(title), "owner_pid": .number(Double(ownerPID)),
            "bundle_id": .string(bundleID),
            "frame": .object(["x": .number(frame.origin.x), "y": .number(frame.origin.y), "width": .number(frame.width), "height": .number(frame.height)]),
            "is_visible": .bool(isVisible), "is_frontmost": .bool(isFrontmost), "display_id": .string(displayID)
        ])
    }
}
struct Snapshot {
    let id: String
    let session: String
    let geometry: DisplayGeometry
    let window: NativeWindow?
    let displayIDs: [String]
    let focus: Int32
    let expires: Date
}

// State lock covers only gates and short paired event posts, never screenshot,
// wait, IPC or PNG encoding. Stop/Pause invalidate queued generations at once.
final class SafetyState {
    private let lock = NSLock()
    private let now: () -> Date
    init(now: @escaping () -> Date = Date.init) { self.now = now }
    private var paused = false
    private var stopped = false
    private var generation = 0
    private var session: String?
    private var observation: Snapshot?
    private var boundTarget: NativeWindow?
    private var requests = Set<String>()
    private var actions = Set<String>()

    func register(_ request: Envelope) throws {
        lock.lock(); defer { lock.unlock() }
        guard request.valid(at: now()) else { throw SafetyError.invalidEnvelope }
        guard !requests.contains(request.requestID) else { throw SafetyError.duplicate }
        guard requests.count < maxRememberedIDs else { stopped = true; throw SafetyError.capacity }
        requests.insert(request.requestID)
    }
    func control(_ command: Command, generation next: Int) throws {
        lock.lock(); defer { lock.unlock() }
        if command == .shutdown || command == .stop {
            stopped = true; paused = true; observation = nil; boundTarget = nil; return
        }
        guard !stopped, next > generation else { throw SafetyError.inactive }
        generation = next; paused = command == .pause; observation = nil
    }
    func end() { lock.lock(); stopped = true; paused = true; observation = nil; boundTarget = nil; lock.unlock() }
    private func check(_ request: Envelope, requireGeneration: Bool = true) throws {
        guard !stopped, !paused else { throw SafetyError.inactive }
        if requireGeneration {
            guard request.payload["generation"]?.integer(in: 0...Int(Int32.max)) == generation else { throw SafetyError.inactive }
        } else if let next = request.payload["generation"]?.integer(in: 0...Int(Int32.max)), next != generation {
            throw SafetyError.inactive
        }
        guard let deadline = request.expires, deadline > now() else { throw SafetyError.expired }
        if let session, session != request.sessionID { throw SafetyError.staleObservation }
    }
    func gate(_ request: Envelope, body: () throws -> Void = {}) throws {
        lock.lock(); defer { lock.unlock() }; try check(request); try body()
    }
    func save(_ snapshot: Snapshot, for request: Envelope) throws {
        lock.lock(); defer { lock.unlock() }; try check(request)
        session = request.sessionID; observation = snapshot
    }
    private func consumeActionLocked(_ request: Envelope) throws {
        guard !actions.contains(request.actionID) else { throw SafetyError.duplicate }
        guard actions.count < maxRememberedIDs else { stopped = true; throw SafetyError.capacity }
        actions.insert(request.actionID)
    }
    func beginControl(_ request: Envelope) throws {
        lock.lock(); defer { lock.unlock() }
        try check(request, requireGeneration: false)
        try consumeActionLocked(request)
    }
    func bindTarget(_ target: NativeWindow, expectedBundleID: String, for request: Envelope) throws {
        lock.lock(); defer { lock.unlock() }
        try check(request, requireGeneration: false)
        guard target.id != "", target.ownerPID > 0, !expectedBundleID.isEmpty,
              target.bundleID == expectedBundleID, target.isVisible,
              target.frame.width > 0, target.frame.height > 0 else { throw SafetyError.targetWindowMismatch }
        if let boundTarget, !boundTarget.matchesIdentity(target) { throw SafetyError.targetWindowMismatch }
        boundTarget = target
        observation = nil
        session = request.sessionID
    }
    func target(for request: Envelope) throws -> NativeWindow? {
        lock.lock(); defer { lock.unlock() }
        try check(request, requireGeneration: false)
        return boundTarget
    }
    func permitted(_ request: Envelope) -> Bool {
        lock.lock(); defer { lock.unlock() }
        do {
            try check(request, requireGeneration: false)
            return true
        } catch {
            return false
        }
    }
    func begin(_ request: Envelope) throws -> Snapshot {
        lock.lock(); defer { lock.unlock() }; try check(request)
        guard let snap = observation, snap.id == request.payload["observation_id"]?.string,
              snap.session == request.sessionID, snap.expires > now() else { throw SafetyError.staleObservation }
        try consumeActionLocked(request)
        observation = nil // consume before dispatch, including rejection
        return snap
    }
}

enum InputOperation {
    case mouse(ActionKind, CGPoint, Int)
    case mouseDown(CGPoint, CGMouseButton)
    case mouseDrag(CGPoint, CGMouseButton)
    case mouseUp(CGPoint, CGMouseButton)
    case unicode([UInt16])
    case key(CGKeyCode, CGEventFlags)
    case scroll(Int32, Int32)
}
struct KeySpec { let code: CGKeyCode; let flags: CGEventFlags }
let keyCodes: [String: CGKeyCode] = ["return":36,"enter":36,"tab":48,"space":49,"delete":51,"backspace":51,"escape":53,"esc":53,
 "left":123,"right":124,"down":125,"up":126,"a":0,"b":11,"c":8,"d":2,"e":14,"f":3,"g":5,"h":4,"i":34,"j":38,"k":40,"l":37,"m":46,
 "n":45,"o":31,"p":35,"q":12,"r":15,"s":1,"t":17,"u":32,"v":9,"w":13,"x":7,"y":16,"z":6,
 "0":29,"1":18,"2":19,"3":20,"4":21,"5":23,"6":22,"7":26,"8":28,"9":25]
let modifierFlags: [String: CGEventFlags] = ["command":.maskCommand,"cmd":.maskCommand,"meta":.maskCommand,
 "control":.maskControl,"ctrl":.maskControl,"shift":.maskShift,"option":.maskAlternate,"alt":.maskAlternate]

func hotkey(_ values: [JSONValue]) throws -> KeySpec {
    guard values.count >= 2 && values.count <= 5 else { throw SafetyError.invalidAction }
    var flags: CGEventFlags = []; var code: CGKeyCode?
    for value in values {
        guard let name = value.string?.lowercased() else { throw SafetyError.invalidAction }
        if let flag = modifierFlags[name] {
            guard !flags.contains(flag) else { throw SafetyError.invalidAction }; flags.insert(flag)
        } else {
            guard code == nil, let key = keyCodes[name] else { throw SafetyError.invalidAction }; code = key
        }
    }
    guard let code, !flags.isEmpty else { throw SafetyError.invalidAction }
    return KeySpec(code: code, flags: flags)
}

struct ActionPlan {
    let operations: [InputOperation]
    let waitMS: Int
    let dragStepDelayMS: Int
    let doubleClick: Bool
    // The point is resolved from the same validated DisplayGeometry used by
    // CGEvent posting. It is metadata only and never drives input.
    let imagePoint: (x: Int, y: Int)?
    let globalPoint: CGPoint?

    private static func button(_ value: JSONValue?) throws -> CGMouseButton {
        switch value?.string?.lowercased() ?? "left" {
        case "left": return .left
        case "right": return .right
        default: throw SafetyError.invalidAction
        }
    }

    init(_ payload: JSONValue, geometry: DisplayGeometry) throws {
        guard let raw = payload["kind"]?.string, let kind = ActionKind(rawValue: raw) else { throw SafetyError.invalidAction }
        var ops: [InputOperation] = []; var wait = 0; var dragDelay = 0
        var imagePoint: (x: Int, y: Int)?
        var globalPoint: CGPoint?
        switch kind {
        case .click, .doubleClick, .rightClick, .move:
            guard let x = payload["x"]?.integer(in: 0...maxImageDimension), let y = payload["y"]?.integer(in: 0...maxImageDimension) else { throw SafetyError.invalidAction }
            let p = try geometry.point(x: x, y: y)
            imagePoint = (x: x, y: y)
            globalPoint = p
            ops.append(.mouse(kind, p, 1)); if kind == .doubleClick { ops.append(.mouse(kind, p, 2)) }
        case .drag:
            guard let startX = payload["start_x"]?.integer(in: 0...maxImageDimension),
                  let startY = payload["start_y"]?.integer(in: 0...maxImageDimension),
                  let endX = payload["x"]?.integer(in: 0...maxImageDimension),
                  let endY = payload["y"]?.integer(in: 0...maxImageDimension) else { throw SafetyError.invalidAction }
            let start = try geometry.point(x: startX, y: startY)
            let end = try geometry.point(x: endX, y: endY)
            let mouseButton = try Self.button(payload["button"])
            let duration = payload["duration_ms"]?.integer(in: 0...maxWaitMS) ?? 0
            let distance = max(abs(endX - startX), abs(endY - startY))
            let steps = max(1, min(maxDragSteps, max(1, distance / 16)))
            ops.append(.mouseDown(start, mouseButton))
            for step in 1...steps {
                let fraction = Double(step) / Double(steps)
                let point = CGPoint(x: start.x + (end.x - start.x) * fraction,
                                    y: start.y + (end.y - start.y) * fraction)
                ops.append(.mouseDrag(point, mouseButton))
            }
            ops.append(.mouseUp(end, mouseButton))
            if duration > 0 { dragDelay = max(1, duration / (steps + 1)) }
        case .type:
            guard let text = payload["text"]?.string, !text.isEmpty, text.utf16.count <= maxTextUnits else { throw SafetyError.invalidAction }
            // One Unicode scalar per down/up pair: no broken surrogate pairs.
            for scalar in text.unicodeScalars { ops.append(.unicode(Array(String(scalar).utf16))) }
        case .key:
            guard let name = payload["key"]?.string?.lowercased(), let code = keyCodes[name] else { throw SafetyError.invalidAction }
            ops.append(.key(code, []))
        case .hotkey:
            guard let values = payload["keys"]?.array else { throw SafetyError.invalidAction }
            let key = try hotkey(values); ops.append(.key(key.code, key.flags))
        case .scroll:
            guard let x = payload["delta_x"]?.integer(in: -maxScrollDelta...maxScrollDelta), let y = payload["delta_y"]?.integer(in: -maxScrollDelta...maxScrollDelta), x != 0 || y != 0 else { throw SafetyError.invalidAction }
            ops.append(.scroll(Int32(x), Int32(y)))
        case .wait:
            guard let duration = payload["duration_ms"]?.integer(in: 0...maxWaitMS) else { throw SafetyError.invalidAction }; wait = duration
        }
        operations = ops; waitMS = wait; dragStepDelayMS = dragDelay; doubleClick = kind == .doubleClick
        self.imagePoint = imagePoint; self.globalPoint = globalPoint
    }
}
