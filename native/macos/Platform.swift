import AppKit
import ApplicationServices
import Carbon.HIToolbox
import CoreGraphics
import ImageIO
import ScreenCaptureKit
import UniformTypeIdentifiers

protocol DesktopPlatform {
    func geometry() throws -> DisplayGeometry
    func geometries() throws -> [DisplayGeometry]
    func windows() throws -> [NativeWindow]
    func allWindows() throws -> [NativeWindow]
    func windowDiagnostics(_ target: NativeWindow) -> [String: Any]
    func validateWindowAccessibility(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult
    func windowGeometry(_ id: String) throws -> DisplayGeometry
    func launchApplication(bundleID: String, permitted: () -> Bool) throws -> NativeWindow
    func activateWindow(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult
    func activeWindowID() -> String?
    func captureAllowed() -> Bool
    func inputAllowed() -> Bool
    func requestPermissions()
    func focus() -> Int32?
    func capture(_ geometry: DisplayGeometry) throws -> Data
    func supportsDrag() -> Bool
    func prepareDrag(_ request: Envelope) throws
    func prepareInput(_ request: Envelope) throws
    func post(_ operation: InputOperation) throws
    func releasePressedButton(_ button: CGMouseButton)
}

extension DesktopPlatform {
    func supportsDrag() -> Bool { true }
    func prepareDrag(_ request: Envelope) throws {}
    func prepareInput(_ request: Envelope) throws {}
    func geometries() throws -> [DisplayGeometry] { [try geometry()] }
    func windows() throws -> [NativeWindow] { [] }
    func allWindows() throws -> [NativeWindow] { try windows() }
    func windowDiagnostics(_ target: NativeWindow) -> [String: Any] { [:] }
    func validateWindowAccessibility(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult {
        AccessibilityWindowActivationResult(success: true, reason: "", axTrusted: true, axWindowCount: 0, geometryMatchCount: 0)
    }
    func windowGeometry(_ id: String) throws -> DisplayGeometry { throw SafetyError.unsupportedDisplay }
    func launchApplication(bundleID: String, permitted: () -> Bool) throws -> NativeWindow { throw SafetyError.launchFailed }
    func activateWindow(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult {
        AccessibilityWindowActivationResult(success: false, reason: "activation_unavailable", axTrusted: false, axWindowCount: 0, geometryMatchCount: 0)
    }
    func activeWindowID() -> String? { nil }
}

struct MacDesktop: DesktopPlatform {
    private let mouseBroker: MouseButtonClient?
    private let diagnostic: ([String: Any]) -> Void

    init(mouseBroker: MouseButtonClient? = nil, diagnostic: @escaping ([String: Any]) -> Void = { _ in }) {
        self.mouseBroker = mouseBroker
        self.diagnostic = diagnostic
    }
    func validateWindowAccessibility(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult {
        AccessibilityWindowActivator.validate(target, permitted: permitted) {
            guard permitted(), self.focus() == target.ownerPID, self.activeWindowID() == target.id,
                  let current = try? self.allWindows().first(where: { $0.id == target.id }),
                  current.matchesIdentity(target), current.isVisible, current.isFrontmost else { return false }
            return true
        }
    }
    func supportsDrag() -> Bool { mouseBroker != nil }

    func prepareDrag(_ request: Envelope) throws {
        guard let mouseBroker, let token = request.payload["drag_token"]?.string else {
            throw SafetyError.inputUnavailable
        }
        try mouseBroker.prepare(token: token)
    }

    func prepareInput(_ request: Envelope) throws {
        guard let rawKind = request.payload["kind"]?.string,
              let kind = ActionKind(rawValue: rawKind) else { throw SafetyError.invalidAction }
        switch kind {
        case .drag:
            try prepareDrag(request)
        case .click, .doubleClick, .rightClick, .type, .key, .hotkey:
            guard let mouseBroker,
                  let token = request.payload["input_batch_token"]?.string,
                  let count = request.payload["input_batch_count"]?.integer(in: MouseButtonClient.batchCountRange) else {
                throw SafetyError.inputUnavailable
            }
            try mouseBroker.prepareBatch(token: token, count: count)
        case .move, .scroll, .wait:
            break
        }
    }

    func geometry() throws -> DisplayGeometry {
        let all = try geometries()
        let mainID = String(CGMainDisplayID())
        guard let main = all.first(where: { $0.id == mainID }) ?? all.first else { throw SafetyError.unsupportedDisplay }
        return main
    }

    func geometries() throws -> [DisplayGeometry] {
        var count: UInt32 = 0
        guard CGGetActiveDisplayList(0, nil, &count) == .success, count > 0 else { throw SafetyError.unsupportedDisplay }
        var ids = [CGDirectDisplayID](repeating: 0, count: Int(count))
        var actual: UInt32 = 0
        guard CGGetActiveDisplayList(count, &ids, &actual) == .success, actual > 0 else { throw SafetyError.unsupportedDisplay }
        let result = try ids.prefix(Int(actual)).map { id -> DisplayGeometry in
            guard let mode = CGDisplayCopyDisplayMode(id) else { throw SafetyError.unsupportedDisplay }
            let geometry = DisplayGeometry(id: String(id), bounds: CGDisplayBounds(id), width: mode.pixelWidth, height: mode.pixelHeight)
            guard geometry.valid else { throw SafetyError.unsupportedDisplay }
            return geometry
        }
        return result
    }
    func windows() throws -> [NativeWindow] {
        try windowInventory(options: [.optionOnScreenOnly, .excludeDesktopElements])
    }

    // Full Window Server inventory is used only for same-target lifecycle
    // recovery and diagnostics. Input/capture still require the target to be
    // raised and present in the normal on-screen inventory before dispatch.
    func allWindows() throws -> [NativeWindow] {
        try windowInventory(options: [.optionAll, .excludeDesktopElements])
    }

    private func windowInventory(options: CGWindowListOption) throws -> [NativeWindow] {
        guard let raw = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else { return [] }
        let activeID = activeWindowID()
        let displays = try geometries()
        return raw.compactMap { entry in
            guard let number = (entry[kCGWindowNumber as String] as? NSNumber)?.uint32Value,
                  let pid = (entry[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value,
                  pid > 0,
                  let boundsDict = entry[kCGWindowBounds as String] as? NSDictionary,
                  let bounds = CGRect(dictionaryRepresentation: boundsDict),
                  bounds.width > 0, bounds.height > 0 else { return nil }
            let layer = (entry[kCGWindowLayer as String] as? NSNumber)?.intValue ?? 0
            let alpha = (entry[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 1
            let title = (entry[kCGWindowName as String] as? String) ?? ""
            let bundle = NSRunningApplication(processIdentifier: pid)?.bundleIdentifier ?? ""
            let displayID = displays.first(where: { $0.bounds.intersects(bounds) })?.id ?? String(CGMainDisplayID())
            return NativeWindow(id: String(number), title: title, ownerPID: pid, bundleID: bundle,
                                frame: bounds, displayID: displayID, isVisible: layer == 0 && alpha > 0,
                                isFrontmost: activeID == String(number))
        }
    }

    // Metadata-only probe. The full inventory is diagnostic evidence, never an
    // input/capture allowlist or an alternative target-binding source.
    func windowDiagnostics(_ target: NativeWindow) -> [String: Any] {
        let raw = CGWindowListCopyWindowInfo([.optionAll, .excludeDesktopElements], kCGNullWindowID) as? [[String: Any]]
        let number = UInt32(target.id)
        let entry = number.flatMap { number in
            raw?.first { ($0[kCGWindowNumber as String] as? NSNumber)?.uint32Value == number }
        }
        let application = NSRunningApplication(processIdentifier: target.ownerPID)
        var fields: [String: Any] = ["inventory_available": raw != nil, "window_present": entry != nil,
            "application_running": application != nil && application?.isTerminated == false]
        if let application {
            fields["application_hidden"] = application.isHidden
            fields["application_active"] = application.isActive
            fields["application_bundle_matches"] = application.bundleIdentifier == target.bundleID
        }
        if let pid = NSWorkspace.shared.frontmostApplication?.processIdentifier {
            fields["workspace_frontmost_pid"] = Int(pid)
        }
        if let pid = focus() { fields["window_order_focus_pid"] = Int(pid) }
        if let entry {
            if let onScreen = entry[kCGWindowIsOnscreen as String] as? NSNumber {
                fields["window_on_screen"] = onScreen.boolValue
            }
            fields["window_owner_matches"] = (entry[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == target.ownerPID
            if let bounds = entry[kCGWindowBounds as String] as? NSDictionary,
               let frame = CGRect(dictionaryRepresentation: bounds) {
                fields["window_frame"] = ["x": frame.minX, "y": frame.minY, "width": frame.width, "height": frame.height]
            }
        }
        return fields
    }

    func windowGeometry(_ id: String) throws -> DisplayGeometry {
        guard let window = try windows().first(where: { $0.id == id }),
              let windowNumber = UInt32(window.id),
              let image = CGWindowListCreateImage(.null, .optionIncludingWindow, CGWindowID(windowNumber), [.bestResolution, .boundsIgnoreFraming]),
              image.width > 0, image.height > 0 else { throw SafetyError.unsupportedDisplay }
        let geometry = DisplayGeometry(id: window.displayID, bounds: window.frame, width: image.width, height: image.height, windowID: window.id)
        guard geometry.valid else { throw SafetyError.unsupportedDisplay }
        return geometry
    }

    /// Sample a captured window image to confirm its content has rendered. A
    /// window can be visible and frontmost before its content is painted
    /// (white frame during launch); returning it then produces a blank
    /// observation and an unstable window_id. Sampling a few pixels is cheaper
    /// than encoding/decoding PNG.
    private func windowHasVisibleContent(_ window: NativeWindow, permitted: () -> Bool) -> Bool {
        guard permitted(),
              let number = UInt32(window.id),
              let image = CGWindowListCreateImage(.null, .optionIncludingWindow, CGWindowID(number), [.bestResolution, .boundsIgnoreFraming]),
              image.width > 0, image.height > 0 else { return false }
        return WindowContentReadiness.hasRenderedContent(image) && permitted()
    }

    func launchApplication(bundleID: String, permitted: () -> Bool) throws -> NativeWindow {
        var dispatched = false
        do {
            return try launchApplicationAndBind(bundleID: bundleID, permitted: permitted) { dispatched = true }
        } catch {
            if dispatched { throw LaunchOperationError(origin: error as? SafetyError ?? .launchFailed) }
            throw error
        }
    }

    private func launchApplicationAndBind(bundleID: String, permitted: () -> Bool, willDispatch: () -> Void) throws -> NativeWindow {
        guard permitted() else { throw SafetyError.inactive }

        do {
            if let running = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).first {
                willDispatch()
                _ = running.activate(options: [.activateAllWindows])
            } else {
                guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: bundleID) else {
                    throw SafetyError.launchFailed
                }
                // Use Launch Services through AppKit. This stays inside the trusted
                // native helper and avoids shell/open/osascript launch paths.
                let configuration = NSWorkspace.OpenConfiguration()
                configuration.activates = true
                configuration.hides = false
                let completion = DispatchSemaphore(value: 0)
                var launchError: Error?
                willDispatch()
                NSWorkspace.shared.openApplication(at: url, configuration: configuration) { _, error in
                    launchError = error
                    completion.signal()
                }
                guard completion.wait(timeout: .now() + 5) == .success else { throw SafetyError.launchTimeout }
                if launchError != nil { throw SafetyError.launchFailed }
            }
        } catch let error as SafetyError {
            throw error
        } catch {
            throw SafetyError.launchFailed
        }

        let deadline = ProcessInfo.processInfo.systemUptime + appLaunchTimeoutSeconds
        var lastInventory = ""
        var stability = LaunchWindowStability()
        var activationDispatched = Set<String>()
        var ambiguous = false
        while ProcessInfo.processInfo.systemUptime < deadline {
            guard permitted() else { throw SafetyError.inactive }
            do {
                let candidates = try allWindows().filter {
                    $0.bundleID == bundleID && $0.isVisible && $0.frame.width > 0 && $0.frame.height > 0
                }
                let frontmost = candidates.filter(\.isFrontmost)
                let inventory = candidates.map { "\($0.id):\($0.ownerPID):\($0.isFrontmost)" }.joined(separator: ",")
                if inventory != lastInventory {
                    diagnostic(["layer": "swift_platform", "phase": "launch_inventory", "bundle_id": bundleID,
                                "candidate_count": candidates.count, "frontmost_count": frontmost.count,
                                "candidates": candidates.map(\.diagnosticFields)])
                    lastInventory = inventory
                }
                let readyCandidates = candidates.filter { windowHasVisibleContent($0, permitted: permitted) }
                let candidate = LaunchWindowStability.candidate(from: readyCandidates)
                let substantialCandidates = LaunchWindowStability.substantialCandidates(from: readyCandidates)
                var activationAttempted = false
                var activationSucceeded = false
                // A temporary frontmost splash can cover a rendered main
                // window. Select only an unambiguous ready window and raise it
                // during launch, never while authorizing input/evidence.
                if let candidate, !candidate.isFrontmost, !activationDispatched.contains(candidate.id) {
                    activationAttempted = true
                    let activation = activateWindow(candidate, permitted: permitted)
                    activationSucceeded = activation.success
                    if activation.actionMayHaveRun { activationDispatched.insert(candidate.id) }
                    if !activation.success && activation.actionMayHaveRun { throw SafetyError.inputUncertain }
                }
                let confirmed = candidate?.isFrontmost == true ? candidate : nil
                let failureReason = LaunchBindingFailureReason.classify(
                    readyCandidateCount: readyCandidates.count,
                    selectedCandidate: candidate != nil,
                    activationAttempted: activationAttempted,
                    activationSucceeded: activationSucceeded,
                    confirmedFrontmost: confirmed != nil)
                if !failureReason.isEmpty {
                    var fields: [String: Any] = [
                        "phase": "launch_binding",
                        "bundle_id": bundleID,
                        "ready_candidate_count": readyCandidates.count,
                        "substantial_candidate_count": substantialCandidates.count,
                        "selected_candidate": candidate != nil,
                        "activation_attempted": activationAttempted,
                        "activation_succeeded": activationSucceeded,
                        "confirmed_frontmost": confirmed != nil,
                        "failure_reason": failureReason,
                    ]
                    if let candidate {
                        fields["selected_window_id"] = candidate.id
                        fields["selected_owner_pid"] = Int(candidate.ownerPID)
                        fields["selected_is_frontmost"] = candidate.isFrontmost
                    }
                    if let pid = focus() { fields["window_order_focus_pid"] = Int(pid) }
                    diagnostic(fields)
                }
                if let stable = stability.update(confirmed, contentReady: confirmed != nil, uptime: ProcessInfo.processInfo.systemUptime) {
                    return stable
                }
                ambiguous = candidate == nil && readyCandidates.count > 1
            } catch let error as SafetyError {
                if error == .targetWindowMismatch || error == .inputUncertain { throw error }
            } catch {
                // Window Server inventory can be briefly unavailable during launch.
            }
            Thread.sleep(forTimeInterval: Double(appLaunchPollMS) / 1000)
        }
        throw ambiguous ? SafetyError.targetWindowMismatch : SafetyError.launchTimeout
    }

    func activateWindow(_ target: NativeWindow, permitted: () -> Bool) -> AccessibilityWindowActivationResult {
        let id = target.id
        func beforeDispatchFailure(_ reason: String) -> AccessibilityWindowActivationResult {
            AccessibilityWindowActivationResult(success: false, reason: reason, axTrusted: AXIsProcessTrusted(), axWindowCount: 0, geometryMatchCount: 0)
        }
        guard permitted() else { return beforeDispatchFailure("activation_not_permitted") }
        let list: [NativeWindow]
        do { list = try allWindows() } catch { return beforeDispatchFailure("window_inventory_unavailable") }
        guard let window = list.first(where: { $0.id == id }), window.ownerPID > 0, window.matchesIdentity(target) else { return beforeDispatchFailure("target_window_unavailable") }
        if focus() == window.ownerPID && (window.isFrontmost || activeWindowID() == id) {
            diagnostic([
                "phase": "window_activation",
                "window_id": id,
                "owner_pid": Int(window.ownerPID),
                "activation_succeeded": true,
                "activation_failure_reason": "already_frontmost",
            ])
            return AccessibilityWindowActivationResult(success: true, reason: "already_frontmost", axTrusted: AXIsProcessTrusted(), axWindowCount: 0, geometryMatchCount: 0)
        }
        let result = AccessibilityWindowActivator.raiseDetailed(window, permitted: permitted) {
            guard let current = try? self.allWindows().first(where: { $0.id == id }) else { return false }
            return current.matchesIdentity(window)
        }
        var fields: [String: Any] = [
            "phase": "window_activation",
            "window_id": id,
            "owner_pid": Int(window.ownerPID),
        ]
        for (key, value) in result.diagnosticFields { fields[key] = value }
        diagnostic(fields)
        return result
    }

    func activeWindowID() -> String? {
        let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
        guard let windows = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] else { return nil }
        // AppKit may expose a separate title-bar/menu companion window above the
        // real content window. Prefer the first substantial layer-0 window so a
        // target's identity remains stable across those decorations.
        var fallback: String?
        for window in windows {
            guard (window[kCGWindowLayer as String] as? NSNumber)?.intValue == 0,
                  let number = (window[kCGWindowNumber as String] as? NSNumber)?.uint32Value else { continue }
            fallback = fallback ?? String(number)
            if let boundsDict = window[kCGWindowBounds as String] as? NSDictionary,
               let bounds = CGRect(dictionaryRepresentation: boundsDict),
               bounds.width > 100, bounds.height > 100 {
                return String(number)
            }
        }
        return fallback
    }

    func captureAllowed() -> Bool { CGPreflightScreenCaptureAccess() }
    func inputAllowed() -> Bool { mouseBroker != nil && AXIsProcessTrusted() && CGPreflightPostEventAccess() }
    func requestPermissions() {
        _ = CGRequestScreenCaptureAccess()
        let options = [kAXTrustedCheckOptionPrompt.takeUnretainedValue() as String: true] as CFDictionary
        _ = AXIsProcessTrustedWithOptions(options)
    }
    // NSWorkspace can report the helper host itself while a different app
    // owns the topmost on-screen window (notably for a non-activating helper
    // launched by a Wails host). Use the Window Server ordering as the
    // authoritative frontmost-app signal, with NSWorkspace as a fallback.
    func focus() -> Int32? {
        let options: CGWindowListOption = [.optionOnScreenOnly, .excludeDesktopElements]
        if let windows = CGWindowListCopyWindowInfo(options, kCGNullWindowID) as? [[String: Any]] {
            for window in windows {
                let layer = (window[kCGWindowLayer as String] as? NSNumber)?.intValue
                guard layer == 0,
                      let pid = (window[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value,
                      pid > 0 else { continue }
                return pid
            }
        }
        return NSWorkspace.shared.frontmostApplication?.processIdentifier
    }
    func capture(_ geometry: DisplayGeometry) throws -> Data {
        guard captureAllowed() else { throw SafetyError.screenshotFailed }
        let image: CGImage?
        if #available(macOS 14.0, *) {
            do {
                image = try screenCaptureKitImage(geometry)
            } catch {
                // Keep the existing Window Server path as a bounded compatibility
                // fallback. Safety validation happens before and after capture in
                // Engine, so switching capture APIs never widens target authority.
                diagnostic(["phase": "screen_capture_kit_fallback", "window_id": geometry.windowID ?? "", "display_id": geometry.id])
                image = legacyCaptureImage(geometry)
            }
        } else {
            image = legacyCaptureImage(geometry)
        }
        guard let image, image.width == geometry.width, image.height == geometry.height else { throw SafetyError.screenshotFailed }
        let data = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil) else { throw SafetyError.screenshotFailed }
        CGImageDestinationAddImage(destination, image, nil)
        guard CGImageDestinationFinalize(destination), data.length <= maxPNGBytes else { throw SafetyError.screenshotFailed }
        return data as Data
    }

    @available(macOS 14.0, *)
    private func screenCaptureKitImage(_ geometry: DisplayGeometry) throws -> CGImage {
        final class CaptureResult: @unchecked Sendable {
            private let lock = NSLock()
            private var image: CGImage?
            private var error: Error?

            func setImage(_ value: CGImage) {
                lock.lock()
                image = value
                lock.unlock()
            }

            func setError(_ value: Error) {
                lock.lock()
                error = value
                lock.unlock()
            }

            func snapshot() -> (CGImage?, Error?) {
                lock.lock()
                defer { lock.unlock() }
                return (image, error)
            }
        }
        let result = CaptureResult()
        let completed = DispatchSemaphore(value: 0)
        Task {
            do {
                let content = try await SCShareableContent.excludingDesktopWindows(false, onScreenWindowsOnly: true)
                let filter: SCContentFilter
                if let windowID = geometry.windowID, let number = UInt32(windowID) {
                    guard let window = content.windows.first(where: { $0.windowID == CGWindowID(number) }) else {
                        throw SafetyError.unsupportedDisplay
                    }
                    filter = SCContentFilter(desktopIndependentWindow: window)
                } else if let displayID = UInt32(geometry.id),
                          let display = content.displays.first(where: { $0.displayID == CGDirectDisplayID(displayID) }) {
                    filter = SCContentFilter(display: display, excludingWindows: [])
                } else {
                    throw SafetyError.unsupportedDisplay
                }
                let configuration = SCStreamConfiguration()
                configuration.width = geometry.width
                configuration.height = geometry.height
                configuration.showsCursor = false
                let image = try await SCScreenshotManager.captureImage(contentFilter: filter, configuration: configuration)
                result.setImage(image)
            } catch {
                result.setError(error)
            }
            completed.signal()
        }
        guard completed.wait(timeout: .now() + 5) == .success else { throw SafetyError.screenshotFailed }
        let (image, error) = result.snapshot()
        if let image { return image }
        if let error { throw error }
        throw SafetyError.screenshotFailed
    }

    private func legacyCaptureImage(_ geometry: DisplayGeometry) -> CGImage? {
        if let windowID = geometry.windowID, let number = UInt32(windowID) {
            return CGWindowListCreateImage(.null, .optionIncludingWindow, CGWindowID(number), [.bestResolution, .boundsIgnoreFraming])
        }
        if let displayID = UInt32(geometry.id) {
            return CGDisplayCreateImage(displayID)
        }
        return nil
    }
    func post(_ operation: InputOperation) throws {
        switch operation {
        case .mouse(let kind, let point, let clickCount):
            if kind == .move {
                let events = try MouseEventSequence.make(kind: kind, point: point, clickCount: clickCount)
                for event in events { event.post(tap: .cghidEventTap) }
            } else {
                guard let mouseBroker else { throw SafetyError.inputUnavailable }
                try mouseBroker.batch()
            }
        case .mouseDown(let point, let button):
            guard let mouseBroker else { throw SafetyError.inputUnavailable }
            try mouseBroker.down(point: point, button: button)
        case .mouseDrag(let point, let button):
            guard let mouseBroker else { throw SafetyError.inputUnavailable }
            try mouseBroker.drag(point: point, button: button)
        case .mouseUp(let point, let button):
            guard let mouseBroker else { throw SafetyError.inputUnavailable }
            try mouseBroker.up(point: point, button: button)
        case .unicode, .key:
            guard let mouseBroker else { throw SafetyError.inputUnavailable }
            try mouseBroker.batch()
        case .scroll(let x, let y):
            guard let event = CGEvent(scrollWheelEvent2Source: nil, units: .pixel, wheelCount: 2, wheel1: y, wheel2: x, wheel3: 0) else { throw SafetyError.inputUnavailable }
            event.flags = []; event.post(tap: .cghidEventTap)
        }
    }

    func releasePressedButton(_ button: CGMouseButton) {
        mouseBroker?.release(button: button)
    }
}

// CGEvent.h specifies modifier down/up events as part of a complete keystroke.
// CGEventCreateKeyboardEvent produces flagsChanged for modifier key codes;
// setting flags on the main key's keyUp alone is not a modifier release.
// Pure construction allows tests to inspect every event without posting input.
enum KeyboardEventSequence {
    static let modifiers: [(code: CGKeyCode, flag: CGEventFlags)] = [
        (CGKeyCode(kVK_Control), .maskControl),
        (CGKeyCode(kVK_Option), .maskAlternate),
        (CGKeyCode(kVK_Shift), .maskShift),
        (CGKeyCode(kVK_Command), .maskCommand),
    ]

    static func make(code: CGKeyCode, flags: CGEventFlags,
                     create: (CGKeyCode, Bool) -> CGEvent? = {
                         CGEvent(keyboardEventSource: nil, virtualKey: $0, keyDown: $1)
                     }) throws -> [CGEvent] {
        var events: [CGEvent] = []
        var held: CGEventFlags = []
        let pressed = modifiers.filter { flags.contains($0.flag) }
        func append(_ key: CGKeyCode, down: Bool, flags: CGEventFlags) throws {
            guard let event = create(key, down) else { throw SafetyError.inputUnavailable }
            event.flags = flags
            events.append(event)
        }
        for modifier in pressed {
            held.insert(modifier.flag)
            try append(modifier.code, down: true, flags: held)
        }
        try append(code, down: true, flags: flags)
        try append(code, down: false, flags: flags)
        for modifier in pressed.reversed() {
            held.remove(modifier.flag)
            try append(modifier.code, down: false, flags: held)
        }
        return events
    }
}

// Construction is independently testable without posting any OS input.
enum MouseEventSequence {
    typealias Factory = (CGEventType, CGPoint, CGMouseButton) -> CGEvent?
    static func make(kind: ActionKind, point: CGPoint, clickCount: Int,
                     create: Factory = { CGEvent(mouseEventSource: nil, mouseType: $0, mouseCursorPosition: $1, mouseButton: $2) }) throws -> [CGEvent] {
        let button: CGMouseButton = kind == .rightClick ? .right : .left
        let down: CGEventType = kind == .move ? .mouseMoved : (button == .right ? .rightMouseDown : .leftMouseDown)
        let types: [CGEventType] = kind == .move ? [down] : [down, button == .right ? .rightMouseUp : .leftMouseUp]
        return try types.map { type in
            guard let event = create(type, point, button) else { throw SafetyError.inputUnavailable }
            event.flags = []
            event.setIntegerValueField(.mouseEventClickState, value: Int64(clickCount))
            return event
        }
    }
}
