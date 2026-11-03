import Foundation
import CoreGraphics

struct ActionResult {
    let outcome: Outcome
    let payload: JSONValue
    let error: SafetyError?
    var dispatchState: DispatchState? = nil
}

private struct CapturedSnapshot {
    let payload: JSONValue
    let geometry: DisplayGeometry
    let window: NativeWindow?
    let focus: Int32
}

typealias NativeDiagnosticWriter = ([String: Any]) -> Void

final class Engine {
    private static let captureAttempts = 3
    private static let captureRetryDelayMS = 50
    let state: SafetyState
    private let platform: DesktopPlatform
    private let now: () -> Date
    private let diagnostic: NativeDiagnosticWriter

    init(platform: DesktopPlatform, now: @escaping () -> Date = Date.init,
         diagnostic: @escaping NativeDiagnosticWriter = { _ in }) {
        self.platform = platform; self.now = now; self.diagnostic = diagnostic; self.state = SafetyState(now: now)
    }

    private func mergedPayload(_ payload: JSONValue, metadata: [String: JSONValue]) -> JSONValue {
        guard !metadata.isEmpty else { return payload }
        guard case .object(var fields) = payload else { return .object(metadata) }
        for (key, value) in metadata { fields[key] = value }
        return .object(fields)
    }

    private func makeVisualMetadata(for plan: ActionPlan) -> [String: JSONValue] {
        guard let image = plan.imagePoint, let global = plan.globalPoint else { return [:] }
        return [
            "actual_point": .object(["x": .number(Double(image.x)), "y": .number(Double(image.y))]),
            "global_point": .object(["x": .number(Double(global.x)), "y": .number(Double(global.y))])
        ]
    }

    private func record(_ request: Envelope, phase: String, error: SafetyError? = nil,
                        fields: [String: Any] = [:]) {
        var event: [String: Any] = [
            "layer": "swift_helper", "phase": phase, "command": request.command,
            "request_id": request.requestID, "action_id": request.actionID, "session_id": request.sessionID,
        ]
        if let error { event["error_code"] = error.rawValue }
        if let targetID = request.payload["target_id"]?.string { event["target_id"] = targetID }
        if let windowID = request.payload["window_id"]?.string { event["requested_window_id"] = windowID }
        if let displayID = request.payload["display_id"]?.string { event["requested_display_id"] = displayID }
        for (key, value) in fields { event[key] = value }
        diagnostic(event)
    }

    private func recordWindowMismatch(_ request: Envelope, phase: String, reason: String,
                                      expected: NativeWindow? = nil, actual: NativeWindow? = nil,
                                      error: SafetyError = .targetWindowMismatch) {
        var fields: [String: Any] = ["reason": reason]
        if let expected {
            fields["expected_window"] = expected.diagnosticFields
            fields["target_lifecycle"] = platform.windowDiagnostics(expected)
        }
        if let actual { fields["actual_window"] = actual.diagnosticFields }
        if let expected, let actual { fields["identity_diffs"] = expected.identityDiffs(actual) }
        record(request, phase: phase, error: error, fields: fields)
    }

    func requestPermissions() -> JSONValue {
        platform.requestPermissions()
        return readiness()
    }

    func readiness() -> JSONValue {
        let geometries = try? platform.geometries()
        let primary = try? platform.geometry()
        let geometry = primary ?? geometries?.first
        let capture = platform.captureAllowed(), input = platform.inputAllowed()
        let displays = geometries?.map { $0.coordinateSpace } ?? []
        let windows = (try? platform.windows())?.map { $0.json } ?? []
        return .object([
            "capture_readiness": .string(geometry == nil ? "unavailable" : (capture ? "ready" : "permission_required")),
            "input_readiness": .string(geometry == nil ? "unavailable" : (input ? "ready" : "permission_required")),
            "permission_state": .string(capture && input ? "approved" : "required"),
            "focus_state": .string(platform.focus() == nil ? "unavailable" : "focused"),
            "image_supported": .bool(geometry != nil), "supports_pause": .bool(true), "supports_stop": .bool(true),
            "coordinate_space": geometry?.coordinateSpace ?? .object(["origin":.string("top_left"),"unit":.string("pixels"),"width":.number(0),"height":.number(0),"scale_factor":.number(0)]),
            "displays": .array(displays),
            "windows": .array(windows),
            "actions": .array(ActionKind.allCases.filter { $0 != .drag || platform.supportsDrag() }.map { .string($0.rawValue) })
        ])
    }
    private func requestedTarget(_ payload: JSONValue) throws -> (id: String, displayName: String, bundleID: String) {
        guard case .object(let fields) = payload,
              let id = fields["target_id"]?.string, !id.isEmpty,
              let displayName = fields["display_name"]?.string, !displayName.isEmpty,
              let bundleID = fields["bundle_id"]?.string, !bundleID.isEmpty,
              fields.keys.allSatisfy({ $0 == "target_id" || $0 == "display_name" || $0 == "bundle_id" || $0 == "generation" }) else {
            throw SafetyError.unsupportedApplication
        }
        // The signed host resolves target IDs to an allowlisted bundle before
        // reaching the helper. The helper still rejects malformed bundle IDs
        // and binds the discovered window to the exact requested identity.
        guard bundleID.split(separator: ".").count >= 2,
              bundleID.utf8.count <= 256 else { throw SafetyError.unsupportedApplication }
        return (id, displayName, bundleID)
    }
    func launchApp(_ request: Envelope) -> ActionResult {
        var launchAcknowledged = false
        do {
            let target = try requestedTarget(request.payload)
            try state.beginControl(request)
            let window = try platform.launchApplication(bundleID: target.bundleID) { self.state.permitted(request) }
            launchAcknowledged = true
            guard window.bundleID == target.bundleID else {
                recordWindowMismatch(request, phase: "launch_bundle_validation", reason: "bundle_id_mismatch", actual: window)
                throw SafetyError.targetWindowMismatch
            }
            let accessibility = platform.validateWindowAccessibility(window) { self.state.permitted(request) }
            guard accessibility.success else {
                record(request, phase: "launch_readiness", error: .launchFailed,
                       fields: accessibility.diagnosticFields.merging(["reason": accessibility.reason,
                                                                        "window": window.diagnosticFields]) { _, new in new })
                throw SafetyError.launchFailed
            }
            try state.bindTarget(window, expectedBundleID: target.bundleID, for: request)
            record(request, phase: "launch_result", fields: ["result": "success", "window": window.diagnosticFields])
            return ActionResult(outcome: .executed, payload: .object([
                "operation": .string(Command.launchApp.rawValue),
                "target_id": .string(target.id),
                "display_name": .string(target.displayName),
                "bundle_id": .string(target.bundleID),
                "window": window.json
            ]), error: nil)
        } catch {
            let operationError = error as? LaunchOperationError
            let safetyError = operationError?.origin ?? (error as? SafetyError ?? .launchFailed)
            record(request, phase: "launch_result", error: safetyError, fields: ["result": "error"])
            if launchAcknowledged || operationError != nil || safetyError == .inputUncertain {
                state.end()
                return ActionResult(outcome: .unknown, payload: .object([:]), error: safetyError, dispatchState: .unknown)
            }
            return ActionResult(outcome: .rejected, payload: .object([:]), error: safetyError)
        }
    }
    private func resolveGeometry(for request: Envelope) throws -> DisplayGeometry {
        if let windowID = request.payload["window_id"]?.string, !windowID.isEmpty {
            return try platform.windowGeometry(windowID)
        }
        let all = try platform.geometries()
        if let display = request.payload["display_id"]?.string, !display.isEmpty {
            guard let selected = all.first(where: { $0.id == display }) else { throw SafetyError.unsupportedDisplay }
            return selected
        }
        return try platform.geometry()
    }
    private func requestedWindow(_ request: Envelope) throws -> NativeWindow? {
        guard let id = request.payload["window_id"]?.string, !id.isEmpty else { return nil }
        guard let window = try platform.allWindows().first(where: { $0.id == id }) else { throw SafetyError.unsupportedDisplay }
        return window
    }
    private func authorizedWindow(_ request: Envelope) throws -> NativeWindow? {
        guard let bound = try state.target(for: request) else { return try requestedWindow(request) }
        guard request.payload["window_id"]?.string == bound.id else {
            recordWindowMismatch(request, phase: "authorized_window", reason: "requested_window_id_mismatch", expected: bound)
            throw SafetyError.targetWindowMismatch
        }
        do {
            guard let current = try requestedWindow(request) else {
                recordWindowMismatch(request, phase: "authorized_window", reason: "requested_window_not_found", expected: bound)
                throw SafetyError.targetWindowMismatch
            }
            let differences = bound.identityDiffs(current)
            guard differences.isEmpty else {
                recordWindowMismatch(request, phase: "authorized_window", reason: "identity_mismatch", expected: bound, actual: current)
                throw SafetyError.targetWindowMismatch
            }
            return current
        } catch let error as SafetyError {
            if error == .unsupportedDisplay {
                let lifecycle = platform.windowDiagnostics(bound)
                let applicationRunning = lifecycle["application_running"] as? Bool
                let windowPresent = lifecycle["window_present"] as? Bool
                if applicationRunning == false && windowPresent == false {
                    recordWindowMismatch(request, phase: "authorized_window", reason: "target_exited_after_launch",
                                          expected: bound, error: .targetExitedAfterLaunch)
                    throw SafetyError.targetExitedAfterLaunch
                }
                recordWindowMismatch(request, phase: "authorized_window", reason: "window_unavailable", expected: bound)
            }
            throw error == .unsupportedDisplay ? SafetyError.targetWindowMismatch : error
        } catch {
            recordWindowMismatch(request, phase: "authorized_window", reason: "window_inventory_error", expected: bound)
            throw SafetyError.targetWindowMismatch
        }
    }

    private func target(_ request: Envelope, geometry: DisplayGeometry) throws {
        if let bound = try state.target(for: request) {
            guard geometry.windowID == bound.id else {
                recordWindowMismatch(request, phase: "target_validation", reason: "geometry_window_id_mismatch", expected: bound,
                                     actual: try? requestedWindow(request))
                throw SafetyError.targetWindowMismatch
            }
            guard try authorizedWindow(request) != nil else { throw SafetyError.targetWindowMismatch }
        } else if let window = request.payload["window_id"]?.string, !window.isEmpty && geometry.windowID != window {
            record(request, phase: "target_validation", error: .unsupportedDisplay,
                   fields: ["reason": "requested_window_geometry_mismatch", "geometry_window_id": geometry.windowID ?? ""])
            throw SafetyError.unsupportedDisplay
        }
        if let display = request.payload["display_id"]?.string, !display.isEmpty && display != geometry.id {
            record(request, phase: "target_validation", error: .unsupportedDisplay,
                   fields: ["reason": "display_id_mismatch", "geometry_display_id": geometry.id])
            throw SafetyError.unsupportedDisplay
        }
    }
    private func displayIDs() throws -> [String] {
        try platform.geometries().map(\.id).sorted()
    }
    // Pure revalidation: never raise/activate a window while checking input or
    // capturing evidence. A focus change must remain visible to the caller.
    private func checkWindow(_ request: Envelope, geometry: DisplayGeometry, expectedWindow: NativeWindow?) throws {
        if let expectedWindow {
            guard let current = try requestedWindow(request) else {
                recordWindowMismatch(request, phase: "check_window", reason: "requested_window_not_found", expected: expectedWindow)
                throw SafetyError.targetWindowMismatch
            }
            let differences = expectedWindow.identityDiffs(current)
            guard differences.isEmpty else {
                recordWindowMismatch(request, phase: "check_window", reason: "identity_mismatch", expected: expectedWindow, actual: current)
                throw SafetyError.targetWindowMismatch
            }
        }
        if let windowID = geometry.windowID, platform.activeWindowID() != windowID {
            record(request, phase: "check_window", error: .focusChanged,
                   fields: ["reason": "active_window_mismatch", "geometry_window_id": windowID,
                            "active_window_id": platform.activeWindowID() ?? ""])
            throw SafetyError.focusChanged
        }
    }
    private func capture(_ request: Envelope, geometry: DisplayGeometry, expectedWindow: NativeWindow? = nil) throws -> CapturedSnapshot {
        try state.gate(request)
        let capOK = platform.captureAllowed()
        // The window position (bounds.origin) can shift between observe and
        // capture by a few pixels during launch animation or Dock placement.
        // Identity (display id, window id, pixel dimensions) must remain
        // stable; use the freshly resolved geometry so the screenshot reflects
        // the window's real position, and reject only if identity changed.
        let resolved = try resolveGeometry(for: request)
        guard capOK,
              resolved.id == geometry.id,
              resolved.windowID == geometry.windowID,
              resolved.width == geometry.width,
              resolved.height == geometry.height else {
            record(request, phase: "capture_precheck", error: .screenshotFailed,
                   fields: ["reason": "geometry_changed", "expected_display_id": geometry.id,
                            "actual_display_id": resolved.id, "expected_window_id": geometry.windowID ?? "",
                            "actual_window_id": resolved.windowID ?? "", "expected_width": geometry.width,
                            "expected_height": geometry.height, "actual_width": resolved.width,
                            "actual_height": resolved.height])
            throw SafetyError.screenshotFailed
        }
        let active = resolved
        try checkWindow(request, geometry: active, expectedWindow: expectedWindow)
        guard let focus = platform.focus() else { throw SafetyError.focusChanged }
        let data = try platform.capture(active)
        try state.gate(request)
        let postCheck = try resolveGeometry(for: request)
        guard postCheck.id == active.id, postCheck.windowID == active.windowID,
              postCheck.width == active.width, postCheck.height == active.height, postCheck.bounds == active.bounds else {
            // Discard a frame captured while the same window moved. Only this
            // position-only case can use the existing bounded read-only retry;
            // dimensions, display and window changes remain terminal here.
            let positionOnly = active.windowID != nil && postCheck.id == active.id && postCheck.windowID == active.windowID &&
                postCheck.width == active.width && postCheck.height == active.height &&
                postCheck.bounds.size == active.bounds.size
            let failure: SafetyError = positionOnly ? .screenshotFailed : .unsupportedDisplay
            record(request, phase: "capture_postcheck", error: failure,
                   fields: ["reason": "geometry_changed_after_capture", "position_only": positionOnly,
                            "window_id": active.windowID ?? "", "post_window_id": postCheck.windowID ?? "",
                            "width": active.width, "height": active.height,
                            "post_width": postCheck.width, "post_height": postCheck.height,
                            "bounds": ["x": active.bounds.minX, "y": active.bounds.minY, "width": active.bounds.width, "height": active.bounds.height],
                            "post_bounds": ["x": postCheck.bounds.minX, "y": postCheck.bounds.minY, "width": postCheck.bounds.width, "height": postCheck.bounds.height]])
            throw failure
        }
        try checkWindow(request, geometry: active, expectedWindow: expectedWindow)
        guard platform.focus() == focus else { throw SafetyError.focusChanged }
        var result: [String: JSONValue] = ["media_type": .string("image/png"), "data": .string(data.base64EncodedString()),
                        "width": .number(Double(active.width)), "height": .number(Double(active.height)),
                        "scale_factor": .number(active.scale), "display_id": .string(active.id),
                        "coordinate_space": active.coordinateSpace]
        let target = try requestedWindow(request)
        if let windowID = active.windowID {
            result["window_id"] = .string(windowID)
            guard let target, target.frame == active.bounds else { throw SafetyError.screenshotFailed }
            if let expectedWindow, !target.matchesIdentity(expectedWindow) { throw SafetyError.targetWindowMismatch }
            result["target_window"] = target.json
        }
        if let frontmost = try? platform.windows().first(where: { $0.isFrontmost }) { result["active_window"] = frontmost.json }
        return CapturedSnapshot(payload: .object(result), geometry: active, window: target, focus: focus)
    }
    private static let activationTimeout = 1.0
    private static let activationPollMS = 20
    private static let activationAttempts = 3
    private static let activationRetryDelayMS = 100

    private func activateTarget(_ window: NativeWindow, request: Envelope) throws {
        guard platform.captureAllowed(), platform.inputAllowed() else { throw SafetyError.permissionRequired }
        guard window.isVisible else { throw SafetyError.unsupportedDisplay }
        var activated = false
        for attempt in 0..<Self.activationAttempts {
            try state.gate(request)
            guard let current = try platform.allWindows().first(where: { $0.id == window.id }),
                  current.matchesIdentity(window), current.isVisible else { throw SafetyError.targetWindowMismatch }
            let activation = platform.activateWindow(window, permitted: {
                do { try self.state.gate(request); return true } catch { return false }
            })
            activated = activation.success
            // Only read-only, pre-dispatch failures may be retried. A missing
            // AX acknowledgement must never automatically raise the window again.
            if activated || activation.actionMayHaveRun { break }
            if attempt + 1 < Self.activationAttempts {
                try wait(Self.activationRetryDelayMS, request: request)
            }
        }
        try state.gate(request)
        guard activated else { throw SafetyError.focusChanged }
        // Activation/AXRaise acknowledgements are asynchronous. Confirm the
        // exact Window Server target, not merely the application's PID.
        let until = ProcessInfo.processInfo.systemUptime + Self.activationTimeout
        while true {
            try state.gate(request)
            guard platform.captureAllowed(), platform.inputAllowed() else { throw SafetyError.permissionRequired }
            if platform.focus() == window.ownerPID && platform.activeWindowID() == window.id { return }
            guard ProcessInfo.processInfo.systemUptime < until else { throw SafetyError.focusChanged }
            try wait(Self.activationPollMS, request: request)
        }
    }

    private func captureObservation(_ request: Envelope, expectedWindow: NativeWindow?) throws -> CapturedSnapshot {
        for attempt in 0..<Self.captureAttempts {
            do {
                let geometry = try resolveGeometry(for: request)
                try target(request, geometry: geometry)
                return try capture(request, geometry: geometry, expectedWindow: expectedWindow)
            } catch let error as SafetyError where error == .screenshotFailed && attempt + 1 < Self.captureAttempts {
                try wait(Self.captureRetryDelayMS * (attempt + 1), request: request)
            }
        }
        throw SafetyError.screenshotFailed
    }

    func observe(_ request: Envelope) -> ActionResult {
        do {
            try state.gate(request)
            let window = try authorizedWindow(request)
            if let window { try activateTarget(window, request: request) }
            guard let id = request.payload["observation_id"]?.string, !id.isEmpty, id.utf8.count <= 256,
                  let focus = platform.focus() else { throw SafetyError.invalidAction }
            let captured = try captureObservation(request, expectedWindow: window)
            guard case .object(var payload) = captured.payload else { throw SafetyError.screenshotFailed }
            guard platform.focus() == focus else { throw SafetyError.focusChanged }
            if let windowID = captured.geometry.windowID, platform.activeWindowID() != windowID { throw SafetyError.focusChanged }
            let expires = now().addingTimeInterval(observationTTLSeconds)
            let topology = try displayIDs()
            try state.save(Snapshot(id: id, session: request.sessionID, geometry: captured.geometry, window: captured.window, displayIDs: topology, focus: captured.focus, expires: expires), for: request)
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            payload["observation_expires_at"] = .string(formatter.string(from: expires))
            if let window = captured.window {
                record(request, phase: "observe_result", fields: ["result": "success", "window": window.diagnosticFields,
                    "target_lifecycle": platform.windowDiagnostics(window)])
            }
            return ActionResult(outcome: .executed, payload: .object(payload), error: nil)
        } catch {
            let safetyError = error as? SafetyError ?? .screenshotFailed
            record(request, phase: "observe_result", error: safetyError, fields: ["result": "error"])
            return ActionResult(outcome: .rejected, payload: .object([:]), error: safetyError)
        }
    }
    private func checkInput(_ snapshot: Snapshot, request: Envelope) throws {
        guard platform.inputAllowed(), platform.captureAllowed() else { throw SafetyError.permissionRequired }
        guard platform.focus() == snapshot.focus else { throw SafetyError.focusChanged }
        try checkWindow(request, geometry: snapshot.geometry, expectedWindow: snapshot.window)
        guard try resolveGeometry(for: request) == snapshot.geometry else { throw SafetyError.unsupportedDisplay }
        guard try displayIDs() == snapshot.displayIDs else { throw SafetyError.unsupportedDisplay }
        // Native discovery can take time. Recheck focus after it, immediately
        // before entering the short event-post gate.
        guard platform.focus() == snapshot.focus else { throw SafetyError.focusChanged }
        try checkWindow(request, geometry: snapshot.geometry, expectedWindow: snapshot.window)
    }
    private func wait(_ milliseconds: Int, request: Envelope) throws {
        let end = ProcessInfo.processInfo.systemUptime + Double(milliseconds) / 1000
        while ProcessInfo.processInfo.systemUptime < end {
            try state.gate(request)
            Thread.sleep(forTimeInterval: min(0.01, max(0, end - ProcessInfo.processInfo.systemUptime)))
        }
        try state.gate(request)
    }
    // Launching an app from the Dock can take longer than a normal input
    // screenshot. Wait for a frontmost-app transition when the click lands in
    // the Dock band, then retry only the screenshot (never the input). This
    // keeps the returned evidence aligned with the actual post-click state
    // without weakening focus, permission, geometry, or topology guards.
    private func waitsForAppActivation(_ request: Envelope, geometry: DisplayGeometry) -> Bool {
        guard let kind = request.payload["kind"]?.string,
              ["click", "double_click", "right_click"].contains(kind),
              let y = request.payload["y"]?.integer(in: 0...maxImageDimension) else { return false }
        return y >= max(0, geometry.height - 220)
    }

    private func waitForAppActivation(_ request: Envelope, geometry: DisplayGeometry) throws {
        guard waitsForAppActivation(request, geometry: geometry), let before = platform.focus() else { return }
        let deadline = ProcessInfo.processInfo.systemUptime + 3.0
        while ProcessInfo.processInfo.systemUptime < deadline {
            try state.gate(request)
            if platform.focus() != before { return }
            try wait(100, request: request)
        }
    }

    private func captureAfterInput(_ request: Envelope, geometry: DisplayGeometry, expectedWindow: NativeWindow?) throws -> JSONValue {
        try waitForAppActivation(request, geometry: geometry)
        for attempt in 0..<Self.captureAttempts {
            do {
                return try capture(request, geometry: geometry, expectedWindow: expectedWindow).payload
            } catch let error as SafetyError where error == .screenshotFailed && attempt + 1 < Self.captureAttempts {
                try wait(Self.captureRetryDelayMS * (attempt + 1), request: request)
            }
        }
        return try capture(request, geometry: geometry, expectedWindow: expectedWindow).payload
    }
    func execute(_ request: Envelope) -> ActionResult {
        var posted = false
        var inputComplete = false
        var actionComplete = false
        var pressedButton: CGMouseButton?
        var visualMetadata: [String: JSONValue] = [:]
        defer { if let pressedButton { platform.releasePressedButton(pressedButton) } }
        do {
            let snapshot = try state.begin(request); try target(request, geometry: snapshot.geometry)
            let plan = try ActionPlan(request.payload, geometry: snapshot.geometry)
            visualMetadata = makeVisualMetadata(for: plan)
            // Passive wait posts no input: a legitimate app launch can change
            // focus after the preceding screenshot. Keep session/generation/
            // expiry binding above and stable-target capture below, but reserve
            // stale-input preflight for operations that actually post events.
            if !plan.operations.isEmpty {
                try checkInput(snapshot, request: request)
                try platform.prepareInput(request)
            }
            for (index, operation) in plan.operations.enumerated() {
                if plan.doubleClick && index > 0 { try wait(60, request: request) }
                // Revalidate TCC/focus/display just before each pair. Stop only
                // waits for this short critical section, not the entire action.
                try checkInput(snapshot, request: request)
                if case .mouseDown(_, let button) = operation { pressedButton = button }
                try state.gate(request) {
                    try platform.post(operation); posted = true
                }
                if case .mouseUp = operation { pressedButton = nil }
                if plan.dragStepDelayMS > 0 && index + 1 < plan.operations.count {
                    try wait(plan.dragStepDelayMS, request: request)
                }
            }
            inputComplete = !plan.operations.isEmpty
            try wait(plan.waitMS, request: request)
            // Dispatch state acknowledges completion of the structured action,
            // not the existence of keyboard/mouse events. A passive wait is
            // complete only after its cancellable timer succeeds. Keep posted
            // separately for partial-input safety and never replay either one.
            actionComplete = true
            // The final atomic operation may legitimately activate another app.
            // Capture requires stable current focus, not the consumed binding;
            // subsequent input still needs a fresh observe. This is not visual
            // verification that the action achieved its intended result.
            let payload = try captureAfterInput(request, geometry: snapshot.geometry, expectedWindow: snapshot.window)
            return ActionResult(outcome: .executed, payload: mergedPayload(payload, metadata: visualMetadata), error: nil,
                                dispatchState: .complete)
        } catch {
            // Explicit Pause/Stop already invalidated the generation. Preserve
            // that state so Pause can Resume with a fresh observation; do not
            // turn cooperative interruption into an irreversible helper stop.
            if (error as? SafetyError) == .inputUncertain { posted = true }
            let safetyError = error as? SafetyError ?? .inputUnavailable
            let acknowledged = inputComplete || actionComplete
            record(request, phase: "execute_result", error: safetyError,
                   fields: ["result": acknowledged ? "executed" : (posted ? "unknown" : "rejected"),
                            "dispatch_state": acknowledged ? "complete" : (posted ? "unknown" : "not_started"), "input_posted": posted])
            if acknowledged && safetyError != .inputUncertain {
                // All input pairs or the passive timer were acknowledged.
                // Losing only evidence must not erase action completion or
                // invent partial/unknown input for an action that posted none.
                return ActionResult(outcome: .executed, payload: .object(visualMetadata), error: safetyError, dispatchState: .complete)
            }
            if posted && safetyError != .inactive { state.end() }
            return ActionResult(outcome: posted ? .unknown : .rejected,
                                payload: posted ? .object(visualMetadata) : .object([:]), error: safetyError,
                                dispatchState: posted ? .unknown : .notStarted)
        }
    }
}
