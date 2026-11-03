import Foundation
import CoreGraphics
import Darwin

let workBuddyAppName = "WorkBuddy"
let workBuddyBundleID = "com.workbuddy.workbuddy"

// Pure fake platform. This executable never constructs MacDesktop or posts a
// CGEvent, asks for permissions, focuses apps, or captures the actual desktop.
final class FakeDesktop: DesktopPlatform {
    var display = DisplayGeometry(id: "1", bounds: CGRect(x: 100, y: -50, width: 100, height: 50), width: 200, height: 100)
    var extraDisplays: [DisplayGeometry] = []
    var targetWindow: NativeWindow?
    var onScreenWindows = true
    var selectedWindowID: String?
    var launchResult: NativeWindow?
    var launchError: SafetyError?
    var launchErrorMayHaveRun = false
    var launchCalls = 0
    var activationCalls = 0
    var activationFailuresRemaining = 0
    var activationFailureMayHaveRun = false
    var activationFailureHook: (() -> Void)?
    var activationHook: (() -> Void)?
    var activeWindowHook: (() -> Void)?
    var windowGeometryHook: (() -> Void)?
    var capturePermission = true
    var inputPermission = true
    var focused: Int32? = 42
    var failCapture = false
    var captureFailuresRemaining = 0
    var failGeometry = false
    var failPost = false
    var losePostAcknowledgement = false
    var posts: [InputOperation] = []
    var postHook: (() -> Void)?
    var captureHook: (() -> Void)?
    var focusHook: (() -> Void)?
    var lifecycle: [String: Any] = [:]
    var lifecycleReads = 0
    var accessibilityReady = true
    func windowDiagnostics(_ target: NativeWindow) -> [String: Any] { lifecycleReads += 1; return lifecycle }
    func validateWindowAccessibility(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult {
        if accessibilityReady {
            return AccessibilityWindowActivationResult(success: true, reason: "", axTrusted: true, axWindowCount: 1, geometryMatchCount: 1)
        }
        return AccessibilityWindowActivationResult(success: false, reason: "ax_window_inventory_failed", axTrusted: true, axWindowCount: 0, geometryMatchCount: 0)
    }
    func geometry() throws -> DisplayGeometry { if failGeometry { throw SafetyError.unsupportedDisplay }; return display }
    func geometries() throws -> [DisplayGeometry] { if failGeometry { throw SafetyError.unsupportedDisplay }; return [display] + extraDisplays }
    func windows() throws -> [NativeWindow] { onScreenWindows ? (targetWindow.map { [$0] } ?? []) : [] }
    func allWindows() throws -> [NativeWindow] { targetWindow.map { [$0] } ?? [] }
    func windowGeometry(_ id: String) throws -> DisplayGeometry {
        windowGeometryHook?()
        guard let window = targetWindow, window.id == id else { throw SafetyError.unsupportedDisplay }
        return DisplayGeometry(id: window.displayID, bounds: window.frame, width: display.width, height: display.height, windowID: window.id)
    }
    func launchApplication(bundleID: String, permitted: () -> Bool) throws -> NativeWindow {
        launchCalls += 1
        guard permitted() else { throw SafetyError.inactive }
        guard bundleID.split(separator: ".").count >= 2 else { throw SafetyError.unsupportedApplication }
        if let launchError {
            if launchErrorMayHaveRun { throw LaunchOperationError(origin: launchError) }
            throw launchError
        }
        guard let launchResult else { throw SafetyError.launchTimeout }
        targetWindow = launchResult
        selectedWindowID = launchResult.id
        focused = launchResult.ownerPID
        return launchResult
    }
    func activateWindow(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult {
        activationCalls += 1
        func result(_ success: Bool, mayHaveRun: Bool = false) -> AccessibilityWindowActivationResult {
            AccessibilityWindowActivationResult(success: success, reason: success ? "" : "fake_failure",
                                                axTrusted: true, axWindowCount: 1, geometryMatchCount: 1,
                                                actionMayHaveRun: mayHaveRun)
        }
        let id = target.id
        guard permitted(), let current = targetWindow, current.matchesIdentity(target) else { return result(false) }
        if activationFailuresRemaining > 0 {
            activationFailuresRemaining -= 1
            activationFailureHook?()
            return result(false, mayHaveRun: activationFailureMayHaveRun)
        }
        selectedWindowID = id
        focused = targetWindow?.ownerPID
        activationHook?()
        return result(true, mayHaveRun: true)
    }
    func activeWindowID() -> String? { activeWindowHook?(); return selectedWindowID }
    func captureAllowed() -> Bool { capturePermission }
    func inputAllowed() -> Bool { inputPermission }
    func requestPermissions() { }
    func focus() -> Int32? { focusHook?(); return focused }
    func capture(_ g: DisplayGeometry) throws -> Data {
        if failCapture || captureFailuresRemaining > 0 {
            if captureFailuresRemaining > 0 { captureFailuresRemaining -= 1 }
            throw SafetyError.screenshotFailed
        }
        captureHook?(); return Data([1,2,3])
    }
    func post(_ operation: InputOperation) throws {
        if failPost { throw SafetyError.inputUnavailable }; posts.append(operation); postHook?()
        if losePostAcknowledgement { throw SafetyError.inputUncertain }
    }
    func releasePressedButton(_ button: CGMouseButton) { posts.append(.mouseUp(.zero, button)) }
}
var assertions = 0
func expect(_ value: @autoclosure () -> Bool, _ label: String) {
    assertions += 1
    if !value() { fputs("FAIL: \(label)\n", stderr); exit(1) }
}
func expectThrows(_ label: String, _ body: () throws -> Void) {
    do { try body(); expect(false, label) } catch { assertions += 1 }
}
func request(_ command: String, payload: [String:JSONValue], session: String = "session", id: String = UUID().uuidString, seconds: Double = 5) -> Envelope {
    let formatter = ISO8601DateFormatter(); formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    var payload = payload; if payload["generation"] == nil { payload["generation"] = .number(0) }
    return Envelope(protocolVersion: protocolVersion, requestID: id, sessionID: session, actionID: id,
                    deadline: formatter.string(from: Date().addingTimeInterval(seconds)), command: command, payload: .object(payload))
}
func setup() -> (Engine,FakeDesktop) {
    let desktop = FakeDesktop(); let engine = Engine(platform: desktop)
    let observation = request("observe", payload: ["observation_id": .string("obs")])
    let result = engine.observe(observation)
    expect(result.outcome == .executed, "fake observation")
    return (engine,desktop)
}
func action(_ kind: String, _ extra: [String:JSONValue] = [:], seconds: Double = 5) -> Envelope {
    var p = extra; p["kind"] = .string(kind); p["observation_id"] = .string("obs")
    return request("execute", payload: p, seconds: seconds)
}

func launchWindow() -> NativeWindow {
    NativeWindow(id: "workbuddy-window", title: "WorkBuddy", ownerPID: 84,
                 bundleID: workBuddyBundleID, frame: CGRect(x: 100, y: -50, width: 100, height: 50),
                 displayID: "1", isVisible: true, isFrontmost: true)
}

// launch_app accepts only a host-resolved target descriptor. The model cannot
// supply a filesystem path; the host registry owns the provider bundle key.
do {
    let desktop = FakeDesktop()
    let engine = Engine(platform: desktop)
    let unknown = engine.launchApp(request("launch_app", payload: ["target_id": .string("safari"), "display_name": .string("Safari"), "bundle_id": .string("Safari"), "generation": .number(0)]))
    expect(unknown.outcome == .rejected && unknown.error == .unsupportedApplication, "launch allowlist rejects unknown app")
    expect(desktop.launchCalls == 0, "unknown app never reaches platform launcher")

    let malformed = engine.launchApp(request("launch_app", payload: [
        "target_id": .string("workbuddy"), "display_name": .string(workBuddyAppName), "generation": .number(0)
    ]))
    expect(malformed.outcome == .rejected && malformed.error == .unsupportedApplication, "launch rejects incomplete trusted target descriptor")
    expect(desktop.launchCalls == 0, "model bundle override never reaches platform launcher")
}

// A visible, pixel-ready window is not enough to authorize a launch. The
// target AX window inventory must also be readable before launch is reported
// executed; otherwise the next provider turn would receive a false receipt.
do {
    let desktop = FakeDesktop()
    desktop.accessibilityReady = false
    desktop.launchResult = launchWindow()
    let engine = Engine(platform: desktop)
    let launched = engine.launchApp(request("launch_app", payload: ["target_id": .string("workbuddy"), "display_name": .string(workBuddyAppName), "bundle_id": .string(workBuddyBundleID), "generation": .number(0)]))
    expect(launched.outcome == .unknown && launched.dispatchState == .unknown && launched.error == .launchFailed,
           "launched app with unavailable AX binding remains unknown")
}

// A successful launch returns an independent receipt payload and stores the
// exact WorkBuddy window identity for subsequent window-bound observations.
do {
    let desktop = FakeDesktop()
    desktop.launchResult = launchWindow()
    let engine = Engine(platform: desktop)
    let launched = engine.launchApp(request("launch_app", payload: ["target_id": .string("workbuddy"), "display_name": .string(workBuddyAppName), "bundle_id": .string(workBuddyBundleID), "generation": .number(0)]))
    expect(launched.outcome == .executed && launched.error == nil, "launch returns executed receipt")
    guard case .object(let payload) = launched.payload,
          payload["target_id"]?.string == "workbuddy",
          payload["bundle_id"]?.string == workBuddyBundleID,
          case .object(let window)? = payload["window"],
          window["id"]?.string == "workbuddy-window",
          window["bundle_id"]?.string == workBuddyBundleID,
          window["owner_pid"]?.integer(in: 1...Int(Int32.max)) == 84,
          window["title"]?.string == "WorkBuddy",
          window["display_id"]?.string == "1",
          window["is_frontmost"]?.bool == true else {
        expect(false, "launch receipt contains WorkBuddy window metadata")
        fatalError("unreachable")
    }
    let observed = engine.observe(request("observe", payload: [
        "observation_id": .string("workbuddy-observation"),
        "window_id": .string("workbuddy-window"),
        "generation": .number(0)
    ]))
    expect(observed.outcome == .executed, "bound WorkBuddy observation succeeds")

    desktop.targetWindow = NativeWindow(id: "workbuddy-window", title: "WorkBuddy", ownerPID: 84,
                                         bundleID: "com.example.other", frame: CGRect(x: 100, y: -50, width: 100, height: 50),
                                         displayID: "1", isVisible: true, isFrontmost: true)
    let mismatch = engine.observe(request("observe", payload: [
        "observation_id": .string("mismatch-observation"),
        "window_id": .string("workbuddy-window"),
        "generation": .number(0)
    ]))
    expect(mismatch.outcome == .rejected && mismatch.error == .targetWindowMismatch,
           "bound window bundle change is rejected")
}

// Lost activation acknowledgements during launch must stay unknown at the
// native protocol boundary, rather than pretend the app was never launched.
do {
    let desktop = FakeDesktop()
    desktop.launchError = .inputUncertain
    let engine = Engine(platform: desktop)
    let result = engine.launchApp(request("launch_app", payload: ["target_id": .string("workbuddy"),
        "display_name": .string(workBuddyAppName), "bundle_id": .string(workBuddyBundleID), "generation": .number(0)]))
    expect(result.outcome == .unknown && result.dispatchState == .unknown && result.error == .inputUncertain,
           "ambiguous native launch never becomes not dispatched")
}

// A launch callback/binding failure after the application-open dispatch is
// uncertain, even if the originating error is launch_timeout.
do {
    let desktop = FakeDesktop()
    desktop.launchError = .launchTimeout
    desktop.launchErrorMayHaveRun = true
    let engine = Engine(platform: desktop)
    let result = engine.launchApp(request("launch_app", payload: ["target_id": .string("workbuddy"),
        "display_name": .string(workBuddyAppName), "bundle_id": .string(workBuddyBundleID), "generation": .number(0)]))
    expect(result.outcome == .unknown && result.error == .launchTimeout,
           "post-dispatch launch timeout must remain unknown with its origin code")
}

// A launch that never produces a visible Window Server target is a controlled
// timeout, not an opaque helper error.
do {
    let desktop = FakeDesktop()
    desktop.launchError = .launchTimeout
    let engine = Engine(platform: desktop)
    let timedOut = engine.launchApp(request("launch_app", payload: ["target_id": .string("workbuddy"), "display_name": .string(workBuddyAppName), "bundle_id": .string(workBuddyBundleID), "generation": .number(0)]))
    expect(timedOut.outcome == .rejected && timedOut.error == .launchTimeout, "launch timeout is controlled")
}

do {
    let desktop = FakeDesktop()
    desktop.extraDisplays = [DisplayGeometry(id: "2", bounds: CGRect(x: -100, y: 0, width: 100, height: 50), width: 200, height: 100)]
    let engine = Engine(platform: desktop)
    let ready = engine.readiness()
    expect(ready["displays"]?.array?.count == 2, "readiness exposes all displays")
    let secondarySpace = ready["displays"]?.array?[1]
    func number(_ value: JSONValue?) -> Double? {
        guard case .number(let value) = value else { return nil }
        return value
    }
    expect(number(secondarySpace?["bounds"]?["x"]) == -100 && number(secondarySpace?["bounds"]?["y"]) == 0,
           "readiness exposes global display origin")
    let result = engine.observe(request("observe", payload: ["observation_id":.string("secondary"), "display_id":.string("2")]))
    expect(result.outcome == .executed, "secondary display observation")
    expect(result.payload["coordinate_space"]?["display_id"]?.string == "2", "capture binds selected display geometry")
    expect(result.payload["coordinate_space"]?["bounds"]?["x"]?.integer(in: -1000...1000) == -100,
           "capture binds negative global origin")
}

do {
    let desktop = FakeDesktop()
    // Software-only mixed-DPI topology: the secondary display has a different
    // pixel/point ratio and a negative global origin.
    desktop.extraDisplays = [DisplayGeometry(id: "3", bounds: CGRect(x: -240, y: 20, width: 100, height: 50), width: 150, height: 75)]
    let engine = Engine(platform: desktop)
    let ready = engine.readiness()
    let mixed = ready["displays"]?.array?.first { $0["display_id"]?.string == "3" }
    let mixedScale: Double? = { guard case .number(let value) = mixed?["scale_factor"] else { return nil }; return value }()
    expect(mixedScale == 1.5, "readiness exposes mixed-DPI scale")
    let observed = engine.observe(request("observe", payload: ["observation_id":.string("mixed-dpi"), "display_id":.string("3")]))
    expect(observed.outcome == .executed, "mixed-DPI display observation")
    let result = engine.execute(request("execute", payload: ["kind":.string("click"), "x":.number(149), "y":.number(74), "display_id":.string("3"), "observation_id":.string("mixed-dpi")]))
    expect(result.outcome == .executed, "mixed-DPI routed click")
    if case .mouse(_, let point, _) = desktop.posts.last! {
        expect(abs(point.x - (-140.6666666667)) < 0.01 && abs(point.y - 69.3333333333) < 0.01, "mixed-DPI point maps through selected display")
    } else { expect(false, "mixed-DPI click posted") }
    expect(result.payload["actual_point"]?["x"]?.integer(in: 0...1000) == 149 &&
           result.payload["actual_point"]?["y"]?.integer(in: 0...1000) == 74,
           "native result retains validated image point")
    if case .number(let globalX)? = result.payload["global_point"]?["x"],
       case .number(let globalY)? = result.payload["global_point"]?["y"] {
        expect(abs(globalX - (-140.6666666667)) < 0.01 && abs(globalY - 69.3333333333) < 0.01,
               "native result retains resolved global point")
    } else { expect(false, "native result includes resolved global point") }
}

do {
    let desktop = FakeDesktop()
    desktop.extraDisplays = [DisplayGeometry(id: "2", bounds: CGRect(x: -100, y: 0, width: 100, height: 50), width: 200, height: 100)]
    let engine = Engine(platform: desktop)
    let observed = engine.observe(request("observe", payload: ["observation_id":.string("topology")]))
    expect(observed.outcome == .executed, "topology baseline observation")
    desktop.extraDisplays = []
    let result = engine.execute(action("click", ["x":.number(2), "y":.number(2)]))
    expect(result.outcome == .rejected && desktop.posts.isEmpty, "display topology change rejects stale action")
}

do {
    let desktop = FakeDesktop()
    desktop.targetWindow = NativeWindow(id: "window-9", title: "Fixture", ownerPID: 77, bundleID: "fixture.app", frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: false)
    let engine = Engine(platform: desktop)
    let observed = engine.observe(request("observe", payload: ["observation_id":.string("window-observation"), "window_id":.string("window-9")]))
    expect(observed.outcome == .executed, "window observation activates and captures target")
    expect(observed.payload["target_window"]?["id"]?.string == "window-9", "capture identifies selected target")
    expect(observed.payload["coordinate_space"]?["bounds"]?["x"]?.integer(in: -1000...1000) == 100,
           "window capture carries target rather than display bounds")
    let actionRequest = request("execute", payload: ["kind":.string("click"), "x":.number(2), "y":.number(2), "window_id":.string("window-9"), "observation_id":.string("window-observation")])
    let actionResult = engine.execute(actionRequest)
    expect(actionResult.outcome == .executed, "window-targeted click")
}

// Identity is not a mutable document title. No input or evidence read may
// reactivate a target and mask a user's focus change.
for change in ["title", "owner", "bundle", "frame", "closed", "focus-during-geometry", "focus-after-input"] {
    let desktop = FakeDesktop()
    let original = NativeWindow(id: "window-10", title: "Fixture", ownerPID: 77, bundleID: "fixture.app", frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: false)
    desktop.targetWindow = original
    let engine = Engine(platform: desktop)
    let observed = engine.observe(request("observe", payload: ["observation_id":.string("window-stale"), "window_id":.string(original.id)]))
    expect(observed.outcome == .executed, "window identity baseline: \(change)")
    let activations = desktop.activationCalls
    if change == "closed" {
        desktop.targetWindow = nil
    } else if change == "focus-during-geometry" {
        desktop.windowGeometryHook = { desktop.selectedWindowID = "other-window" }
    } else if change == "focus-after-input" {
        desktop.postHook = { desktop.selectedWindowID = "other-window" }
    } else {
        desktop.targetWindow = NativeWindow(id: original.id, title: change == "title" ? "Next page" : original.title,
            ownerPID: change == "owner" ? 78 : original.ownerPID, bundleID: change == "bundle" ? "other.app" : original.bundleID,
            frame: change == "frame" ? original.frame.offsetBy(dx: 1, dy: 0) : original.frame,
            displayID: original.displayID, isVisible: true, isFrontmost: false)
    }
    let result = engine.execute(request("execute", payload: ["kind":.string("move"), "x":.number(2), "y":.number(2), "window_id":.string(original.id), "observation_id":.string("window-stale")]))
    let expected: Outcome = change == "title" || change == "focus-after-input" ? .executed : .rejected
    expect(result.outcome == expected, "target outcome: \(change)")
    expect(desktop.posts.count == (change == "title" || change == "focus-after-input" ? 1 : 0), "target input count: \(change)")
    expect(desktop.activationCalls == activations, "Execute never activates window: \(change)")
}

// A false AX activation result is not proof that NSRunningApplication/AXRaise
// had no effects. Never automatically re-dispatch an ambiguous activation.
do {
    let desktop = FakeDesktop()
    desktop.targetWindow = NativeWindow(id: "ambiguous-activation", title: "Fixture", ownerPID: 77,
        bundleID: "fixture.app", frame: desktop.display.bounds, displayID: desktop.display.id,
        isVisible: true, isFrontmost: false)
    desktop.activationFailuresRemaining = 3
    desktop.activationFailureMayHaveRun = true
    let engine = Engine(platform: desktop)
    let result = engine.observe(request("observe", payload: ["observation_id": .string("ambiguous-frame"),
                                                             "window_id": .string("ambiguous-activation")]))
    expect(result.outcome == .rejected && result.error == .focusChanged, "ambiguous activation fails closed")
    expect(desktop.activationCalls == 1, "unknown AX activation is never automatically replayed")
    expect(desktop.posts.isEmpty && result.payload["data"] == nil, "ambiguous activation authorizes no input or screenshot")
}

// Even a safe pre-dispatch retry must not follow a recycled Window Server ID
// to another owner while waiting. The original exact identity is immutable.
do {
    let desktop = FakeDesktop()
    let original = NativeWindow(id: "recycled-window", title: "Fixture", ownerPID: 77,
        bundleID: "fixture.app", frame: desktop.display.bounds, displayID: desktop.display.id,
        isVisible: true, isFrontmost: false)
    desktop.targetWindow = original
    desktop.activationFailuresRemaining = 1
    desktop.activationFailureHook = {
        desktop.targetWindow = NativeWindow(id: original.id, title: original.title, ownerPID: 88,
            bundleID: "other.app", frame: original.frame, displayID: original.displayID,
            isVisible: true, isFrontmost: false)
    }
    let engine = Engine(platform: desktop)
    let result = engine.observe(request("observe", payload: ["observation_id": .string("recycled-frame"),
                                                             "window_id": .string(original.id)]))
    expect(result.outcome == .rejected && desktop.activationCalls == 1,
           "activation retry never follows a window ID to another owner")
    expect(desktop.posts.isEmpty && result.payload["data"] == nil, "recycled activation identity authorizes no input")
}

// Two same-title windows must be selected by geometry, never inventory order.
do {
    let frame = CGRect(x: 50, y: 75, width: 400, height: 300)
    let target = NativeWindow(id: "target", title: "Same title", ownerPID: 77, bundleID: "fixture", frame: frame, displayID: "1", isVisible: true, isFrontmost: false)
    let match = AccessibleWindowDescriptor(frame: frame, title: target.title, minimized: false)
    let other = AccessibleWindowDescriptor(frame: frame.offsetBy(dx: 60, dy: 50), title: target.title, minimized: false)
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: [other, match]) == 1, "same-title windows use geometry")
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: [match, other]) == 0, "selection independent of inventory order")
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: [match, match]) == nil, "identical candidates fail closed")
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: [other]) == nil, "title alone cannot select target")
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: []) == nil, "missing candidate fails closed")
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: [AccessibleWindowDescriptor(frame: frame, title: target.title, minimized: true)]) == nil, "minimized target rejected")
    expect(WindowTargetSelector.uniqueIndex(for: target, candidates: [AccessibleWindowDescriptor(frame: frame, title: "Wrong", minimized: false)]) == nil, "conflicting title rejected")
    expect(!WindowTargetSelector.framesMatch(frame, CGRect(x: CGFloat.infinity, y: 75, width: 400, height: 300)), "nonfinite AX geometry rejected")
    expect(!WindowTargetSelector.framesMatch(frame, CGRect(x: 50, y: 75, width: 0, height: 300)), "empty AX geometry rejected")
}

// Observe waits for the exact raised ID, and cancellation wins while settling.
for canceled in [false, true] {
    let desktop = FakeDesktop()
    let window = NativeWindow(id: "settling-target", title: "Fixture", ownerPID: 77, bundleID: "fixture.app", frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: false)
    desktop.targetWindow = window
    let engine = Engine(platform: desktop)
    var reads = 0
    desktop.activationHook = { desktop.selectedWindowID = "previous-window" }
    desktop.activeWindowHook = {
        reads += 1
        if reads == 2 {
            if canceled { try? engine.state.control(.pause, generation: 1) }
            else { desktop.selectedWindowID = window.id }
        }
    }
    let result = engine.observe(request("observe", payload: ["observation_id":.string("settling"), "window_id":.string(window.id)]))
    expect(result.outcome == (canceled ? .rejected : .executed), "activation settle observes exact window/cancellation")
    expect(desktop.activationCalls == 1 && desktop.posts.isEmpty, "activation does not retry raise or post input")
}

// Same-window activation may transiently lose the Window Server race. The
// bounded recovery retries only activation, never input dispatch, and keeps
// the exact target identity/generation checks in the helper.
do {
    let desktop = FakeDesktop()
    let window = NativeWindow(id: "retry-target", title: "Fixture", ownerPID: 78, bundleID: "fixture.app", frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: false)
    desktop.targetWindow = window
    desktop.activationFailuresRemaining = 1
    let engine = Engine(platform: desktop)
    let result = engine.observe(request("observe", payload: ["observation_id":.string("retry-observation"), "window_id":.string(window.id)]))
    expect(result.outcome == .executed && desktop.activationCalls == 2 && desktop.posts.isEmpty,
           "same-window activation recovers without dispatching input")
}

// Safe integer conversion, overflow, bounds, NaN, infinity, fractions.
for n in [Double.nan, Double.infinity, -Double.infinity, 1e99, -1e99, 1.5, -1, 10001] {
    expect(JSONValue.number(n).integer(in: 0...10000) == nil, "invalid integer \(n)")
}
expect(JSONValue.number(Double(Int.max)).integer(in: 0...Int.max) == nil, "Int boundary rounding")
expect(JSONValue.number(10000).integer(in: 0...10000) == 10000, "inclusive bound")

let retina = FakeDesktop().display
let p = try retina.point(x: 198, y: 98)
expect(p.x == 199 && p.y == -1 && retina.scale == 2, "Retina pixels plus display origin")
expectThrows("off-display point") { _ = try retina.point(x: 200, y: 0) }
expectThrows("negative point") { _ = try retina.point(x: -1, y: 0) }
for payload in [
    ["kind": JSONValue.string("wait"), "duration_ms": .number(1e99)],
    ["kind": .string("scroll"), "delta_x": .number(1e99), "delta_y": .number(1)],
    ["kind": .string("click"), "x": .number(1.5), "y": .number(0)],
    ["kind": .string("type"), "text": .string(String(repeating: "x", count: maxTextUnits + 1))]
] { expectThrows("action numeric/text bound") { _ = try ActionPlan(.object(payload), geometry: retina) } }

let combo = try hotkey([.string("cmd"), .string("shift"), .string("c")])
expect(combo.code == 8 && combo.flags.contains(.maskCommand) && combo.flags.contains(.maskShift), "modifier flags")
for keys in [["cmd","NOT-A-KEY","c"],["cmd","command","c"],["a","b"],["cmd"],["c"],["cmd","shift"],["cmd","c","d"]] {
    expectThrows("invalid hotkey must not partially execute") { _ = try hotkey(keys.map{.string($0)}) }
}
let unicode = try ActionPlan(.object(["kind":.string("type"),"text":.string("A😀")]), geometry: retina)
expect(unicode.operations.count == 2, "Unicode scalar grouping")
if case .unicode(let units) = unicode.operations[1] { expect(units.count == 2, "surrogate pair preserved") } else { expect(false,"Unicode op") }
let click = try ActionPlan(.object(["kind":.string("double_click"),"x":.number(2),"y":.number(2)]), geometry: retina)
expect(click.operations.count == 2, "double click pair count")
if case .mouse(_, _, let count) = click.operations[1] { expect(count == 2, "double click state") }
let drag = try ActionPlan(.object(["kind":.string("drag"),"start_x":.number(10),"start_y":.number(10),"x":.number(190),"y":.number(90),"duration_ms":.number(160)]), geometry: retina)
expect(drag.operations.count >= 4, "drag has down, path, and up")
if case .mouseDown = drag.operations.first! { expect(true, "drag mouse down") } else { expect(false, "drag mouse down") }
if case .mouseUp = drag.operations.last! { expect(true, "drag mouse up") } else { expect(false, "drag mouse up") }
expect(drag.dragStepDelayMS > 0, "drag duration creates pacing")

do {
    let (engine, desktop) = setup()
    var postCount = 0
    desktop.postHook = {
        postCount += 1
        if postCount == 1 { desktop.focused = 7 }
    }
    let result = engine.execute(action("drag", ["start_x":.number(10), "start_y":.number(10), "x":.number(190), "y":.number(90), "duration_ms":.number(320)]))
    expect(result.outcome == .unknown && result.error == .focusChanged, "drag focus interruption is ambiguous")
    expect(desktop.posts.contains { if case .mouseUp = $0 { return true }; return false }, "interrupted drag releases mouse button")
}

// Cooperative interruption releases before returning; Pause remains resumable
// only through an explicit new generation + fresh observation, never replay.
for command in [Command.pause, .stop] {
    let (engine, desktop) = setup()
    var interrupted = false
    desktop.focusHook = {
        if !interrupted && desktop.posts.count == 1 {
            interrupted = true
            try? engine.state.control(command, generation: 1)
        }
    }
    let result = engine.execute(action("drag", ["start_x":.number(10),"start_y":.number(10),"x":.number(190),"y":.number(90),"duration_ms":.number(3000)]))
    expect(result.outcome == .unknown && result.error == .inactive, "interrupted drag reports unknown, no replay")
    expect(desktop.posts.count == 2, "interrupted drag posts only down and cleanup up")
    if case .mouseUp = desktop.posts.last! { expect(true, "cleanup up before execute return") } else { expect(false,"missing cleanup release") }
    desktop.focusHook = nil
    if command == .pause {
        try engine.state.control(.resume, generation: 2)
        expect(engine.execute(action("key",["key":.string("return"),"generation":.number(2)])).outcome == .rejected, "resume invalidates interrupted observation")
        expect(engine.observe(request("observe",payload:["observation_id":.string("resumed-drag"),"generation":.number(2)])).outcome == .executed, "pause drag can resume with fresh capture")
    } else {
        expectThrows("stop drag cannot resume") { try engine.state.control(.resume,generation:2) }
    }
}

for reason in ["permission","capture","focus","geometry","noevent","stopped","paused","expired","session","observation","display","window","generation"] {
    let (engine,desktop) = setup()
    var req = action("type", ["text":.string("abc")])
    switch reason {
    case "permission": desktop.inputPermission = false
    case "capture": desktop.capturePermission = false
    case "focus": desktop.focused = 7
    case "geometry": desktop.failGeometry = true
    case "noevent": desktop.failPost = true
    case "stopped": try engine.state.control(.stop, generation: 1)
    case "paused": try engine.state.control(.pause, generation: 1)
    case "expired": req = action("type", ["text":.string("abc")], seconds: -1)
    case "session": req = request("execute", payload:["kind":.string("type"),"text":.string("abc"),"observation_id":.string("obs")], session:"other")
    case "observation": req = request("execute", payload:["kind":.string("type"),"text":.string("abc"),"observation_id":.string("old")])
    case "display": req = action("type",["text":.string("abc"),"display_id":.string("2")])
    case "window": req = action("type",["text":.string("abc"),"window_id":.string("9")])
    default: req = action("type",["text":.string("abc"),"generation":.number(2)])
    }
    let result = engine.execute(req)
    expect(result.outcome == .rejected && desktop.posts.isEmpty, "pre-input rejection: \(reason)")
}

// A completed atomic action may activate another app. Its after-image must
// reflect the new focus; further input still requires a fresh observation.
let activatedFocus: Int32 = 7
let activationActions: [(String, [String:JSONValue])] = [
    ("hotkey", ["keys":.array([.string("command"),.string("tab")])]),
    ("key", ["key":.string("return")]),
    ("click", ["x":.number(2),"y":.number(2)]),
    ("type", ["text":.string("ab")]),
    ("double_click", ["x":.number(2),"y":.number(2)])
]
for (kind, payload) in activationActions {
    let (engine,desktop) = setup()
    let originalFocus = desktop.focused
    let operationCount = try ActionPlan(action(kind, payload).payload, geometry: desktop.display).operations.count
    desktop.postHook = {
        if desktop.posts.count == operationCount { desktop.focused = activatedFocus }
    }
    var capturedFocus: Int32?
    desktop.captureHook = { capturedFocus = desktop.focused }
    let result = engine.execute(action(kind, payload))
    expect(result.outcome == .executed && result.error == nil && desktop.posts.count == operationCount,
           "terminal focus switch accepted: \(kind)")
    expect(capturedFocus == activatedFocus && result.payload["data"]?.string == Data([1,2,3]).base64EncodedString(),
           "fresh after-image captured at activated focus: \(kind)")
    expect(engine.execute(action("key", ["key":.string("return")])).outcome == .rejected && desktop.posts.count == operationCount,
           "activation does not revive consumed observation: \(kind)")
    desktop.postHook = nil
    let nextObservation = "activated"
    expect(engine.observe(request("observe", payload:["observation_id":.string(nextObservation)])).outcome == .executed,
           "helper remains usable after activation: \(kind)")
    let next = request("execute", payload:["kind":.string("key"),"key":.string("return"),"observation_id":.string(nextObservation)])
    expect(engine.execute(next).outcome == .executed && desktop.posts.count == operationCount + 1,
           "fresh observation binds to activated focus: \(kind)")
    expect(engine.observe(request("observe", payload:["observation_id":.string(nextObservation)])).outcome == .executed,
           "observe activated focus again: \(kind)")
    desktop.focused = originalFocus
    let staleFocus = engine.execute(request("execute", payload:["kind":.string("key"),"key":.string("return"),"observation_id":.string(nextObservation)]))
    expect(staleFocus.outcome == .rejected && staleFocus.error == .focusChanged && desktop.posts.count == operationCount + 1,
           "new binding rejects old focus before dispatch: \(kind)")
}

// A Dock click waits for a delayed app activation before publishing after-image evidence.
do {
    let (engine, desktop) = setup()
    var focusReads = 0
    var capturedFocus: Int32?
    desktop.postHook = {
        desktop.focusHook = {
            focusReads += 1
            if focusReads >= 3 { desktop.focused = 7 }
        }
    }
    desktop.captureHook = { capturedFocus = desktop.focused }
    let result = engine.execute(action("click", ["x": .number(2), "y": .number(99)]))
    expect(result.outcome == .executed && desktop.posts.count == 1 && capturedFocus == 7,
           "Dock activation waits before publishing after-image")
}

// A transient post-input screenshot failure is retried without replaying input.
do {
    let (engine, desktop) = setup()
    desktop.captureFailuresRemaining = 2
    let result = engine.execute(action("click", ["x": .number(2), "y": .number(2)]))
    expect(result.outcome == .executed && desktop.posts.count == 1, "transient after-image capture retries without replay")
}

// Switching before dispatch must reject even an action intended to activate an app.
do {
    let (engine,desktop) = setup()
    desktop.focused = activatedFocus
    let result = engine.execute(action("hotkey", ["keys":.array([.string("command"),.string("tab")])]))
    expect(result.outcome == .rejected && result.error == .focusChanged && desktop.posts.isEmpty,
           "activation action rejects stale focus before dispatch")
}

// A switch between atomic operations must never send the rest into another app.
for (kind, payload) in activationActions where kind == "type" || kind == "double_click" {
    let (engine,desktop) = setup()
    desktop.postHook = { desktop.focused = activatedFocus }
    let result = engine.execute(action(kind, payload))
    expect(result.outcome == .unknown && result.dispatchState == .unknown && result.error == .focusChanged && desktop.posts.count == 1,
           "focus switch between operations aborts: \(kind)")
    expectThrows("ambiguous multi-operation action cannot resume: \(kind)") { try engine.state.control(.resume,generation:1) }
}

// A new stable focus is acceptable; a change while capturing the after-image
// (including focus loss) is ambiguous and must still stop the helper.
for loseFocus in [false, true] {
    let (engine,desktop) = setup()
    let originalFocus = desktop.focused
    var captured = false
    desktop.postHook = { desktop.focused = activatedFocus }
    desktop.captureHook = {
        captured = true
        desktop.focused = loseFocus ? nil : originalFocus
    }
    let result = engine.execute(action("key", ["key":.string("return")]))
    expect(result.outcome == .executed && result.dispatchState == .complete && result.error == .focusChanged && desktop.posts.count == 1,
           "focus changes during after-capture fail closed: \(loseFocus)")
    expect(captured && result.payload["data"] == nil, "inconsistent after-image is captured but not returned")
    try engine.state.control(.resume,generation:1)
    expect(true, "confirmed complete input may recover observation")
    expect(engine.observe(request("observe", payload:["observation_id":.string("after-failure")])).outcome == .rejected,
           "inconsistent after-capture prevents new binding")
}

// No focused app is not a valid new binding, even after a completed action.
do {
    let (engine,desktop) = setup()
    var captured = false
    desktop.postHook = { desktop.focused = nil }
    desktop.captureHook = { captured = true }
    let result = engine.execute(action("key", ["key":.string("return")]))
    expect(result.outcome == .executed && result.dispatchState == .complete && result.error == .focusChanged && desktop.posts.count == 1 && !captured,
           "missing after-focus fails before capture")
    try engine.state.control(.resume,generation:1)
    expect(true, "complete input with missing focus is not partial input")
}

for reason in ["permission","capture","focus","geometry","screenshot","deadline"] {
    let (engine,desktop) = setup()
    desktop.postHook = {
        switch reason {
        case "permission": desktop.inputPermission = false
        case "capture": desktop.capturePermission = false
        case "focus": desktop.focused = activatedFocus
        case "geometry": desktop.failGeometry = true
        case "screenshot": desktop.failCapture = true
        default: Thread.sleep(forTimeInterval: 0.05)
        }
    }
    let result = engine.execute(action("type", ["text":.string(reason == "screenshot" ? "a" : "ab")], seconds: reason == "deadline" ? 0.03 : 5))
    if reason == "screenshot" {
        expect(result.outcome == .executed && result.dispatchState == .complete && desktop.posts.count == 1, "complete input preserves acknowledged dispatch")
        try engine.state.control(.resume,generation:1)
    } else {
        expect(result.outcome == .unknown && desktop.posts.count == 1, "post-input uncertainty: \(reason)")
        expectThrows("unknown outcome cannot resume") { try engine.state.control(.resume,generation:1) }
    }
}

do {
    let (engine,desktop) = setup()
    let req = action("hotkey",["keys":.array([.string("command"),.string("c")])])
    expect(engine.execute(req).outcome == .executed && desktop.posts.count == 1, "hotkey on fake platform")
    expect(engine.execute(req).outcome == .rejected, "consumed observation cannot replay")
    expect(engine.observe(request("observe",payload:["observation_id":.string("next")])).outcome == .executed, "fresh observe works")
    expect(engine.observe(request("observe",payload:["observation_id":.string("other")],session:"other")).outcome == .rejected, "session binding sticky")
}
// Pause/Resume invalidate both old observations and queued generation-0 work.
do {
    let (engine,desktop) = setup()
    try engine.state.control(.pause,generation:1); try engine.state.control(.resume,generation:2)
    expect(engine.execute(action("type",["text":.string("x")])).outcome == .rejected && desktop.posts.isEmpty, "stale queued generation")
    expect(engine.observe(request("observe",payload:["observation_id":.string("new"),"generation":.number(2)])).outcome == .executed, "observe after resume")
    expectThrows("out of order control") { try engine.state.control(.pause,generation:1) }
    try engine.state.control(.stop,generation:3)
    expectThrows("stop cannot resume") { try engine.state.control(.resume,generation:4) }
}
// Waiting is passive. App launch may change frontmost PID after the last
// observation; this must not be confused with posting stale-coordinate input.
do {
    let (engine, desktop) = setup()
    desktop.focused = 84
    let result = engine.execute(action("wait", ["duration_ms": .number(1)]))
    expect(result.outcome == .executed && result.dispatchState == .complete, "completed passive wait acknowledges action without input")
    expect(desktop.posts.isEmpty, "passive wait never posts native input")
}

do {
    let (engine, desktop) = setup()
    desktop.captureHook = { desktop.focused = 84 }
    let result = engine.execute(action("wait", ["duration_ms": .number(1)]))
    expect(result.outcome == .executed && result.dispatchState == .complete && result.error == .focusChanged && result.payload["data"] == nil,
           "completed wait keeps acknowledgement but rejects inconsistent screenshot")
    expect(desktop.posts.isEmpty, "failed wait capture never posts input")
}

// The passive timer completed even when only the after-image fails. No input
// was posted, no wait is replayed, and a fresh observation is still required.
do {
    let (engine, desktop) = setup()
    desktop.failCapture = true
    let result = engine.execute(action("wait", ["duration_ms": .number(1)]))
    expect(result.outcome == .executed && result.dispatchState == .complete && result.error == .screenshotFailed,
           "wait screenshot failure preserves completed action")
    expect(desktop.posts.isEmpty && result.payload["data"] == nil, "wait evidence failure never posts input or publishes a bad image")
    expect(engine.execute(action("wait", ["duration_ms": .number(1)])).outcome == .rejected,
           "completed wait cannot reuse consumed observation")
}

// A stop on the reader thread can interrupt a long wait on the worker.
do {
    let (engine,_) = setup()
    let done = DispatchSemaphore(value:0)
    let began = DispatchSemaphore(value:0)
    DispatchQueue.global().async {
        began.signal()
        let result = engine.execute(action("wait",["duration_ms":.number(5000)]))
        expect(result.outcome == .rejected && result.dispatchState == .notStarted, "interrupted wait never completed or posted input")
        done.signal()
    }
    began.wait(); Thread.sleep(forTimeInterval:0.03)
    let start = Date(); try engine.state.control(.stop,generation:1)
    expect(done.wait(timeout:.now()+0.5) == .success && Date().timeIntervalSince(start)<0.5,"Stop not blocked by wait")
}
// Envelope deadlines, duplicate request IDs and malformed JSON never input.
do {
    let state = SafetyState()
    let req = request("readiness",payload:[:])
    try state.register(req)
    expectThrows("duplicate request ID") { try state.register(req) }
    expectThrows("expired deadline") { try state.register(request("execute",payload:[:],seconds:-1)) }
    expectThrows("unbounded deadline") { try state.register(request("execute",payload:[:],seconds:1000)) }
    let huge = Data("{\"x\":1e999}".utf8)
    expectThrows("overflow JSON number") { _ = try JSONDecoder().decode(JSONValue.self,from:huge) }
}

// Capture RPC deadlines and snapshot freshness are independent. Use an injected
// clock, not sleeps, to prove both RPC-expired/fresh and snapshot-expired cases.
for expiredSnapshot in [false, true] {
    var instant = Date()
    let desktop = FakeDesktop()
    let engine = Engine(platform: desktop, now: { instant })
    let observed = engine.observe(request("observe", payload: ["observation_id": .string("obs")], seconds: 1))
    expect(observed.outcome == .executed, "bounded capture RPC completed")
    expect(observed.payload["observation_expires_at"]?.string != nil, "helper reports actual snapshot expiry")
    instant = instant.addingTimeInterval(expiredSnapshot ? observationTTLSeconds + 1 : 2)
    let result = engine.execute(action("click", ["x": .number(2), "y": .number(2)], seconds: 60))
    if expiredSnapshot {
        expect(result.outcome == .rejected && desktop.posts.isEmpty, "expired snapshot rejected despite fresh action RPC")
    } else {
        expect(result.outcome == .executed && desktop.posts.count == 1, "snapshot survives completed capture RPC deadline")
    }
}

do {
    let (engine, desktop) = setup()
    desktop.losePostAcknowledgement = true
    let result = engine.execute(action("drag", ["start_x": .number(2), "start_y": .number(2), "x": .number(30), "y": .number(30)]))
    expect(result.outcome == .unknown, "down posted with lost host ACK is unknown, not rejected")
    expect(desktop.posts.count == 2, "lost down ACK still runs release cleanup")
}

// Diagnostics identify the failing native gate without carrying titles/text.
do {
    let desktop = FakeDesktop()
    var events: [[String: Any]] = []
    let engine = Engine(platform: desktop, diagnostic: { events.append($0) })
    let window = launchWindow()
    desktop.launchResult = window
    let launch = engine.launchApp(request("launch_app", payload: [
        "target_id": .string("fixture"), "display_name": .string("Fixture"),
        "bundle_id": .string(window.bundleID)
    ]))
    expect(launch.outcome == .executed, "diagnostic baseline launch")
    desktop.targetWindow = NativeWindow(id: window.id, title: "PRIVATE TITLE", ownerPID: window.ownerPID + 1,
        bundleID: window.bundleID, frame: window.frame, displayID: window.displayID, isVisible: true, isFrontmost: true)
    let result = engine.observe(request("observe", payload: ["window_id": .string(window.id), "observation_id": .string("diag-obs")]))
    expect(result.outcome == .rejected && result.error == .targetWindowMismatch, "diagnostic identity rejection")
    let mismatch = events.first { ($0["phase"] as? String) == "authorized_window" }
    expect((mismatch?["identity_diffs"] as? [String]) == ["owner_pid"], "diagnostic exact mismatch field")
    expect(mismatch?["request_id"] != nil && mismatch?["session_id"] != nil, "diagnostic correlated envelope")
    let encoded = try JSONSerialization.data(withJSONObject: events)
    expect(!String(decoding: encoded, as: UTF8.self).contains("PRIVATE TITLE"), "diagnostic excludes window titles")
}

// Launch readiness ignores window chrome and requires stable rendered content.
do {
    let context = CGContext(data: nil, width: 100, height: 100, bitsPerComponent: 8, bytesPerRow: 400,
        space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)!
    context.setFillColor(CGColor(gray: 1, alpha: 1)); context.fill(CGRect(x: 0, y: 0, width: 100, height: 100))
    expect(!WindowContentReadiness.hasRenderedContent(context.makeImage()!), "white content is not ready")
    context.setFillColor(CGColor(gray: 0, alpha: 1)); context.fill(CGRect(x: 0, y: 95, width: 100, height: 5))
    expect(!WindowContentReadiness.hasRenderedContent(context.makeImage()!), "titlebar/chrome does not make white content ready")
    context.fill(CGRect(x: 30, y: 40, width: 40, height: 10))
    expect(WindowContentReadiness.hasRenderedContent(context.makeImage()!), "rendered body variation is ready")
    var stability = LaunchWindowStability()
    let window = launchWindow()
    expect(stability.update(window, contentReady: true, uptime: 1) == nil, "first sample never binds")
    expect(stability.update(window, contentReady: true, uptime: 1.11)?.id == window.id, "stable ready identity binds")
    expect(stability.update(window, contentReady: false, uptime: 1.2) == nil, "blank resets stability")
    expect(stability.update(window, contentReady: true, uptime: 1.3) == nil, "readiness must stabilize again")
    let other = NativeWindow(id: "other", title: "", ownerPID: window.ownerPID, bundleID: window.bundleID,
        frame: window.frame, displayID: window.displayID, isVisible: true, isFrontmost: true)
    expect(LaunchWindowStability.candidate(from: [window, other]) == nil, "ambiguous frontmost fails selection")
    let rendered = NativeWindow(id: "rendered-main", title: "", ownerPID: window.ownerPID, bundleID: window.bundleID,
        frame: CGRect(x: 0, y: 35, width: 1200, height: 792), displayID: window.displayID, isVisible: true, isFrontmost: false)
    expect(LaunchWindowStability.candidate(from: [rendered])?.id == rendered.id, "unique rendered main can be raised instead of binding blank splash")
    let menuBar = NativeWindow(id: "menu-bar", title: "", ownerPID: window.ownerPID, bundleID: window.bundleID,
        frame: CGRect(x: 0, y: 0, width: 1352, height: 34), displayID: window.displayID, isVisible: true, isFrontmost: false)
    expect(LaunchWindowStability.candidate(from: [rendered, menuBar])?.id == rendered.id,
           "window chrome must not make a ready main window ambiguous")
    expect(LaunchBindingFailureReason.classify(readyCandidateCount: 1, selectedCandidate: true,
        activationAttempted: true, activationSucceeded: false, confirmedFrontmost: false) == "window_activation_failed",
        "failed window activation must be diagnostically distinct from missing content")
    expect(LaunchBindingFailureReason.classify(readyCandidateCount: 2, selectedCandidate: false,
        activationAttempted: false, activationSucceeded: false, confirmedFrontmost: false) == "ambiguous_ready_windows",
        "multiple ready windows must remain an ambiguity")
}

// Snapshot geometry must be the geometry actually used to capture, not an
// earlier lookup. Input still rejects motion after that fresh snapshot.
do {
    let desktop = FakeDesktop()
    let original = NativeWindow(id: "geometry-fresh", title: "Fixture", ownerPID: 77, bundleID: "fixture.app",
        frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: true)
    desktop.targetWindow = original
    var lookups = 0
    desktop.windowGeometryHook = {
        lookups += 1
        if lookups == 2 {
            desktop.targetWindow = NativeWindow(id: original.id, title: original.title, ownerPID: original.ownerPID,
                bundleID: original.bundleID, frame: original.frame.offsetBy(dx: 1, dy: 0), displayID: original.displayID,
                isVisible: true, isFrontmost: true)
        }
    }
    let engine = Engine(platform: desktop)
    let observed = engine.observe(request("observe", payload: ["window_id": .string(original.id), "observation_id": .string("fresh-geometry")]))
    expect(observed.outcome == .executed, "observe uses newly captured geometry")
    let moved = engine.execute(request("execute", payload: ["kind": .string("move"), "x": .number(2), "y": .number(2),
        "window_id": .string(original.id), "observation_id": .string("fresh-geometry")]))
    expect(moved.outcome == .executed && desktop.posts.count == 1, "input uses actual capture geometry without stale-origin rejection")
}

// Read-only lifecycle evidence must distinguish an off-screen window from destruction.
do {
    let desktop = FakeDesktop()
    var events: [[String: Any]] = []
    let engine = Engine(platform: desktop, diagnostic: { events.append($0) })
    let window = launchWindow()
    desktop.launchResult = window
    _ = engine.launchApp(request("launch_app", payload: ["target_id": .string("fixture"),
        "display_name": .string("Fixture"), "bundle_id": .string(window.bundleID)]))
    desktop.lifecycle = ["inventory_available": true, "window_present": true,
        "window_on_screen": false, "application_running": true, "application_hidden": true,
        "workspace_frontmost_pid": 84, "window_order_focus_pid": window.ownerPID]
    desktop.targetWindow = nil
    let result = engine.observe(request("observe", payload: ["window_id": .string(window.id),
        "observation_id": .string("off-screen")]))
    let mismatch = events.first { ($0["reason"] as? String) == "window_unavailable" }
    let lifecycle = mismatch?["target_lifecycle"] as? [String: Any]
    expect(lifecycle?["window_present"] as? Bool == true && lifecycle?["window_on_screen"] as? Bool == false,
           "off-screen diagnostic retains all-window presence")
    expect(lifecycle?["application_hidden"] as? Bool == true && lifecycle?["workspace_frontmost_pid"] as? Int == 84,
           "diagnostic separates application state from window order")
    expect(result.outcome == .rejected && result.error == .targetWindowMismatch && desktop.posts.isEmpty,
           "diagnostics never authorize off-screen input")
    expect(desktop.activationCalls == 0, "lifecycle probe cannot reactivate a missing window")
}

// A bound target whose application has exited is a distinct terminal target
// lifecycle failure. It must not be collapsed into a transient focus/window
// mismatch, because the caller must never replay launch automatically.
do {
    let desktop = FakeDesktop()
    let engine = Engine(platform: desktop)
    let window = launchWindow()
    desktop.launchResult = window
    _ = engine.launchApp(request("launch_app", payload: ["target_id": .string("fixture"),
        "display_name": .string("Fixture"), "bundle_id": .string(window.bundleID)]))
    desktop.lifecycle = ["inventory_available": true, "window_present": false,
        "application_running": false, "window_on_screen": false]
    desktop.targetWindow = nil
    let result = engine.observe(request("observe", payload: ["window_id": .string(window.id),
        "observation_id": .string("target-exited")]))
    expect(result.outcome == .rejected && result.error?.rawValue == "target_exited_after_launch",
           "exited target must have a non-replayable lifecycle error")
}

// A bound target may temporarily leave the on-screen inventory while the
// app/Space is being restored. Authorization must use the full inventory, but
// the target identity remains exact and no alternate window is accepted.
do {
    let desktop = FakeDesktop()
    let window = launchWindow()
    desktop.targetWindow = window
    desktop.selectedWindowID = window.id
    desktop.focused = window.ownerPID
    desktop.onScreenWindows = false
    let engine = Engine(platform: desktop)
    let observed = engine.observe(request("observe", payload: ["window_id": .string(window.id),
        "observation_id": .string("full-inventory-target")]))
    expect(observed.outcome == .executed, "full inventory can find same bound target")
    expect(desktop.posts.isEmpty, "off-screen recovery lookup never posts input")
}

// Motion during a capture invalidates that image, not the stable target identity.
do {
    let desktop = FakeDesktop()
    let original = NativeWindow(id: "capture-motion", title: "PRIVATE", ownerPID: 77, bundleID: "fixture.app",
        frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: true)
    desktop.targetWindow = original; desktop.selectedWindowID = original.id; desktop.focused = original.ownerPID
    var captures = 0
    desktop.captureHook = {
        captures += 1
        if captures == 1 {
            desktop.targetWindow = NativeWindow(id: original.id, title: original.title, ownerPID: original.ownerPID,
                bundleID: original.bundleID, frame: original.frame.offsetBy(dx: 1, dy: 0), displayID: original.displayID,
                isVisible: true, isFrontmost: true)
        }
    }
    let engine = Engine(platform: desktop)
    let observed = engine.observe(request("observe", payload: ["window_id": .string(original.id),
        "observation_id": .string("motion-settled")]))
    expect(observed.outcome == .executed && captures == 2, "origin-only drift retries capture, never input")
    let result = engine.execute(request("execute", payload: ["kind": .string("move"), "x": .number(2), "y": .number(2),
        "window_id": .string(original.id), "observation_id": .string("motion-settled")]))
    expect(result.outcome == .executed && desktop.posts.count == 1, "settled capture binds the actual current origin")
}

// The same read-only retry is bounded and cannot swallow real capture-layout changes.
for change in ["continuous_motion", "dimensions", "display", "owner", "stop"] {
    let desktop = FakeDesktop()
    let original = NativeWindow(id: "capture-strict", title: "PRIVATE", ownerPID: 77, bundleID: "fixture.app",
        frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: true)
    desktop.targetWindow = original; desktop.selectedWindowID = original.id; desktop.focused = original.ownerPID
    let engine = Engine(platform: desktop)
    var captures = 0
    desktop.captureHook = {
        captures += 1
        if change == "dimensions" { desktop.display = DisplayGeometry(id: desktop.display.id,
            bounds: desktop.display.bounds, width: desktop.display.width + 1, height: desktop.display.height) }
        if change == "stop" { try? engine.state.control(.stop, generation: 1) }
        desktop.targetWindow = NativeWindow(id: original.id, title: original.title,
            ownerPID: change == "owner" ? original.ownerPID + 1 : original.ownerPID,
            bundleID: original.bundleID,
            frame: change == "continuous_motion" ? original.frame.offsetBy(dx: CGFloat(captures), dy: 0) : original.frame,
            displayID: change == "display" ? "2" : original.displayID, isVisible: true, isFrontmost: true)
    }
    let observed = engine.observe(request("observe", payload: ["window_id": .string(original.id),
        "observation_id": .string("strict-layout")]))
    expect(observed.outcome == .rejected && desktop.posts.isEmpty, "capture drift never authorizes input: \(change)")
    expect(captures == (change == "continuous_motion" ? 3 : 1), "retry remains bounded and position-only: \(change)")
}

// Recovery after a completed input must retry only the after-image, not that input.
do {
    let desktop = FakeDesktop()
    let original = NativeWindow(id: "after-motion", title: "PRIVATE", ownerPID: 77, bundleID: "fixture.app",
        frame: desktop.display.bounds, displayID: desktop.display.id, isVisible: true, isFrontmost: true)
    desktop.targetWindow = original; desktop.selectedWindowID = original.id; desktop.focused = original.ownerPID
    let engine = Engine(platform: desktop)
    _ = engine.observe(request("observe", payload: ["window_id": .string(original.id), "observation_id": .string("before-motion")]))
    var captures = 0
    desktop.captureHook = {
        captures += 1
        if captures == 1 { desktop.targetWindow = NativeWindow(id: original.id, title: original.title,
            ownerPID: original.ownerPID, bundleID: original.bundleID, frame: original.frame.offsetBy(dx: 1, dy: 0),
            displayID: original.displayID, isVisible: true, isFrontmost: true) }
    }
    let result = engine.execute(request("execute", payload: ["kind": .string("key"), "key": .string("a"),
        "window_id": .string(original.id), "observation_id": .string("before-motion")]))
    expect(result.outcome == .executed && result.error == nil && result.dispatchState == .complete,
           "after-image motion preserves complete dispatch")
    expect(captures == 2 && desktop.posts.count == 1, "after-image retry cannot replay input")
}

// AX failures must identify the exact read stage without exposing window content.
do {
    let result = AccessibilityWindowActivationResult(success: false, reason: "ax_window_unreadable",
        axTrusted: true, axWindowCount: 1, geometryMatchCount: 0,
        failureStage: "window.position.read", failureAttribute: "AXPosition",
        axErrorCode: -25205, axValueTypeID: 42)
    let fields = result.diagnosticFields
    expect(fields["ax_failure_stage"] as? String == "window.position.read",
           "AX diagnostics preserve the exact failing stage")
    expect(fields["ax_failure_attribute"] as? String == "AXPosition",
           "AX diagnostics preserve the failing attribute name")
    expect(fields["ax_error_code"] as? Int == -25205,
           "AX diagnostics preserve the raw AXError code")
    expect(fields["ax_value_type_id"] as? UInt64 == 42,
           "AX diagnostics preserve the CF type ID only")
}

// Exact Window Server identity/frontmost state may be used only for the
// narrowly identified AX geometry-read incompatibility. Permission, unknown
// stages, and already-dispatched effects must remain fail-closed.
do {
    let geometryReadFailure = AccessibilityWindowActivationResult(success: false, reason: "ax_window_unreadable",
        axTrusted: true, axWindowCount: 1, geometryMatchCount: 0,
        failureStage: "window.position.read", failureAttribute: "AXPosition", axErrorCode: -25205)
    expect(AccessibilityWindowFallbackPolicy.allowsExactWindowServerFallback(geometryReadFailure),
           "AX geometry-read incompatibility permits exact Window Server fallback")
    let permissionFailure = AccessibilityWindowActivationResult(success: false, reason: "ax_not_trusted",
        axTrusted: false, axWindowCount: 0, geometryMatchCount: 0)
    expect(!AccessibilityWindowFallbackPolicy.allowsExactWindowServerFallback(permissionFailure),
           "untrusted accessibility never falls back")
    let unknownFailure = AccessibilityWindowActivationResult(success: false, reason: "ax_window_unreadable",
        axTrusted: true, axWindowCount: 1, geometryMatchCount: 0,
        failureStage: "application.windows.values", failureAttribute: "AXWindows", actionMayHaveRun: true)
    expect(!AccessibilityWindowFallbackPolicy.allowsExactWindowServerFallback(unknownFailure),
           "already-dispatched AX uncertainty never falls back or replays")
}


// Explicit diagnostics must never follow a symlink to an unrelated file.
do {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("niuma-diagnostics-")
        .appendingPathComponent(UUID().uuidString)
    let target = root.appendingPathComponent("target.log")
    let link = root.appendingPathComponent("diagnostics.log")
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true,
                                            attributes: [.posixPermissions: 0o700])
    try Data("sentinel\n".utf8).write(to: target, options: .atomic)
    try FileManager.default.createSymbolicLink(at: link, withDestinationURL: target)
    setenv("NIUMA_COMPUTER_DIAGNOSTICS_PATH", link.path, 1)
    NativeDiagnostics.append(["phase": "symlink-regression"])
    let contents = try String(contentsOf: target, encoding: .utf8)
    expect(contents == "sentinel\n", "diagnostics reject symlink destinations")
    try? FileManager.default.removeItem(at: root)
}

print("PASS: \(assertions) native safety assertions (fake platform; no real input/capture)")
