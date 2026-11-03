import AppKit
import ApplicationServices
import CoreGraphics

struct AccessibleWindowDescriptor {
    let frame: CGRect
    let title: String
    let minimized: Bool
}

// Public AX has no public Window Server ID getter. Match within the already
// selected process, require geometry (never title alone), and reject ambiguity.
// Verify the actual Window Server ID after raising; a match is not authority.
enum WindowTargetSelector {
    static let geometryTolerance: CGFloat = 0.5

    static func matchingIndices(for target: NativeWindow, candidates: [AccessibleWindowDescriptor]) -> [Int] {
        guard target.isVisible, target.ownerPID > 0 else { return [] }
        return candidates.indices.filter { index in
            let candidate = candidates[index]
            return !candidate.minimized && framesMatch(target.frame, candidate.frame) &&
                (target.title.isEmpty || candidate.title == target.title)
        }
    }

    static func uniqueIndex(for target: NativeWindow, candidates: [AccessibleWindowDescriptor]) -> Int? {
        let matches = matchingIndices(for: target, candidates: candidates)
        return matches.count == 1 ? matches.first : nil
    }

    static func framesMatch(_ lhs: CGRect, _ rhs: CGRect) -> Bool {
        let pairs = [(lhs.minX, rhs.minX), (lhs.minY, rhs.minY),
                     (lhs.width, rhs.width), (lhs.height, rhs.height)]
        return lhs.width > 0 && lhs.height > 0 && rhs.width > 0 && rhs.height > 0 &&
            pairs.allSatisfy { $0.0.isFinite && $0.1.isFinite && abs($0.0 - $0.1) <= geometryTolerance }
    }
}

// Keep the native AX adapter separate from the identity selection policy so
// matching and ambiguity are tested without touching the user's desktop.
struct AccessibilityWindowActivationResult {
    let success: Bool
    let reason: String
    let axTrusted: Bool
    let axWindowCount: Int
    let geometryMatchCount: Int
    // Read-stage metadata is deliberately limited to public AX identifiers
    // and type IDs. It must never contain title, text, image, or other window
    // content.
    let failureStage: String?
    let failureAttribute: String?
    let axErrorCode: Int?
    let axValueTypeID: UInt64?
    // A false return after activate/AXRaise is ambiguous, not permission to
    // replay. Read-only inspection failures leave this false.
    var actionMayHaveRun = false

    init(success: Bool, reason: String, axTrusted: Bool, axWindowCount: Int, geometryMatchCount: Int,
         failureStage: String? = nil, failureAttribute: String? = nil,
         axErrorCode: Int? = nil, axValueTypeID: UInt64? = nil,
         actionMayHaveRun: Bool = false) {
        self.success = success
        self.reason = reason
        self.axTrusted = axTrusted
        self.axWindowCount = axWindowCount
        self.geometryMatchCount = geometryMatchCount
        self.failureStage = failureStage
        self.failureAttribute = failureAttribute
        self.axErrorCode = axErrorCode
        self.axValueTypeID = axValueTypeID
        self.actionMayHaveRun = actionMayHaveRun
    }

    var diagnosticFields: [String: Any] {
        var fields: [String: Any] = [
            "activation_succeeded": success,
            "activation_failure_reason": reason,
            "ax_trusted": axTrusted,
            "ax_window_count": axWindowCount,
            "ax_geometry_match_count": geometryMatchCount,
            "activation_may_have_run": actionMayHaveRun,
        ]
        if let failureStage { fields["ax_failure_stage"] = failureStage }
        if let failureAttribute { fields["ax_failure_attribute"] = failureAttribute }
        if let axErrorCode { fields["ax_error_code"] = axErrorCode }
        if let axValueTypeID { fields["ax_value_type_id"] = axValueTypeID }
        return fields
    }
}

enum AccessibilityWindowFallbackPolicy {
    // These stages indicate that AX exposed the application/window object but
    // could not provide geometry metadata. The exact Window Server identity is
    // already selected by MacDesktop; fallback remains safe only when the
    // caller rechecks that same ID/PID/bundle is frontmost before use.
    private static let exactWindowServerStages: Set<String> = [
        "application.windows.count",
        "window.position.read", "window.position.type", "window.position.value",
        "window.size.read", "window.size.type", "window.size.value",
        "window.minimized.read",
    ]

    static func allowsExactWindowServerFallback(_ result: AccessibilityWindowActivationResult) -> Bool {
        guard !result.success, !result.actionMayHaveRun,
              result.reason == "ax_window_unreadable", result.axTrusted,
              let stage = result.failureStage else { return false }
        return exactWindowServerStages.contains(stage)
    }
}

enum AccessibilityWindowActivator {
    static let messagingTimeout: Float = 0.1
    static let maxWindows = 128

    private struct Inspection {
        let result: AccessibilityWindowActivationResult
        let element: AXUIElement?
    }

    static func validate(_ target: NativeWindow, permitted: () -> Bool, stillCurrent: () -> Bool) -> AccessibilityWindowActivationResult {
        let inspection = inspect(target, permitted: permitted)
        if !inspection.result.success {
            // Validation never activates or raises. When only AX geometry is
            // unavailable, the caller may prove the exact Window Server
            // identity/frontmost state through stillCurrent instead.
            guard AccessibilityWindowFallbackPolicy.allowsExactWindowServerFallback(inspection.result),
                  permitted(), stillCurrent() else { return inspection.result }
            return AccessibilityWindowActivationResult(success: true, reason: "window_server_validation",
                                                       axTrusted: inspection.result.axTrusted,
                                                       axWindowCount: inspection.result.axWindowCount,
                                                       geometryMatchCount: 1)
        }
        guard permitted(), stillCurrent() else {
            return AccessibilityWindowActivationResult(success: false, reason: "binding_changed_before_validate",
                                                       axTrusted: inspection.result.axTrusted,
                                                       axWindowCount: inspection.result.axWindowCount,
                                                       geometryMatchCount: inspection.result.geometryMatchCount)
        }
        return inspection.result
    }

    static func raise(_ target: NativeWindow, permitted: () -> Bool, stillCurrent: () -> Bool) -> Bool {
        raiseDetailed(target, permitted: permitted, stillCurrent: stillCurrent).success
    }

    static func raiseDetailed(_ target: NativeWindow, permitted: () -> Bool, stillCurrent: () -> Bool) -> AccessibilityWindowActivationResult {
        let inspection = inspect(target, permitted: permitted)
        if !inspection.result.success {
            guard AccessibilityWindowFallbackPolicy.allowsExactWindowServerFallback(inspection.result),
                  permitted(), stillCurrent(), let app = NSRunningApplication(processIdentifier: target.ownerPID) else {
                return inspection.result
            }
            // AppKit activation is itself a dispatched side effect. It is
            // intentionally the only fallback; Engine must confirm the exact
            // Window Server ID before any input/capture and never retry it.
            var metadata = inspection.result
            metadata.actionMayHaveRun = true
            let activated = app.activate(options: [.activateAllWindows])
            guard permitted() else {
                return AccessibilityWindowActivationResult(success: false, reason: "permission_revoked_after_app_activation",
                                                           axTrusted: metadata.axTrusted,
                                                           axWindowCount: metadata.axWindowCount,
                                                           geometryMatchCount: metadata.geometryMatchCount,
                                                           failureStage: metadata.failureStage,
                                                           failureAttribute: metadata.failureAttribute,
                                                           axErrorCode: metadata.axErrorCode,
                                                           axValueTypeID: metadata.axValueTypeID,
                                                           actionMayHaveRun: true)
            }
            guard activated else {
                return AccessibilityWindowActivationResult(success: false, reason: "app_activation_failed",
                                                           axTrusted: metadata.axTrusted,
                                                           axWindowCount: metadata.axWindowCount,
                                                           geometryMatchCount: metadata.geometryMatchCount,
                                                           failureStage: metadata.failureStage,
                                                           failureAttribute: metadata.failureAttribute,
                                                           axErrorCode: metadata.axErrorCode,
                                                           axValueTypeID: metadata.axValueTypeID,
                                                           actionMayHaveRun: true)
            }
            return AccessibilityWindowActivationResult(success: true, reason: "window_server_activation_fallback",
                                                       axTrusted: metadata.axTrusted,
                                                       axWindowCount: metadata.axWindowCount,
                                                       geometryMatchCount: 1,
                                                       actionMayHaveRun: true)
        }
        guard let element = inspection.element else { return inspection.result }
        var metadata = inspection.result
        func failure(_ reason: String) -> AccessibilityWindowActivationResult {
            AccessibilityWindowActivationResult(success: false, reason: reason,
                                                axTrusted: metadata.axTrusted,
                                                axWindowCount: metadata.axWindowCount,
                                                geometryMatchCount: metadata.geometryMatchCount,
                                                actionMayHaveRun: metadata.actionMayHaveRun)
        }
        guard permitted(), stillCurrent(), permitted(), let app = NSRunningApplication(processIdentifier: target.ownerPID) else {
            return failure("binding_changed_before_raise")
        }
        // Bring the same application window back to the active Space before
        // AXRaise. This is a same-PID/window recovery, not a target rebind.
        metadata.actionMayHaveRun = true
        _ = app.activate(options: [.activateAllWindows])
        // Do not retry AX actions after timeout/unknown effects. The Engine will
        // require the requested ID to become frontmost before capture/input.
        guard permitted() else { return failure("permission_revoked_before_raise") }
        guard AXUIElementPerformAction(element, kAXRaiseAction as CFString) == .success else {
            return failure("ax_raise_failed")
        }
        return metadata
    }

    private struct ReadFailure: Error {
        let stage: String
        let attribute: String?
        let errorCode: Int?
        let valueTypeID: UInt64?
    }

    private struct AttributeRead {
        let value: CFTypeRef?
        let errorCode: Int?
        let permissionRevoked: Bool
    }

    private static func inspect(_ target: NativeWindow, permitted: () -> Bool) -> Inspection {
        func failure(_ reason: String, axTrusted: Bool = false, axWindowCount: Int = 0,
                     geometryMatchCount: Int = 0, read: ReadFailure? = nil) -> Inspection {
            Inspection(result: AccessibilityWindowActivationResult(
                success: false, reason: reason, axTrusted: axTrusted,
                axWindowCount: axWindowCount, geometryMatchCount: geometryMatchCount,
                failureStage: read?.stage, failureAttribute: read?.attribute,
                axErrorCode: read?.errorCode, axValueTypeID: read?.valueTypeID), element: nil)
        }
        guard permitted() else { return failure("permission_revoked") }
        let axTrusted = AXIsProcessTrusted()
        guard axTrusted else { return failure("ax_not_trusted") }
        guard target.ownerPID > 0 else { return failure("target_pid_invalid", axTrusted: axTrusted) }
        let appElement = AXUIElementCreateApplication(target.ownerPID)
        let timeoutError = AXUIElementSetMessagingTimeout(appElement, messagingTimeout)
        guard timeoutError == .success else {
            return failure("ax_app_timeout_setup_failed", axTrusted: axTrusted,
                           read: ReadFailure(stage: "application.timeout", attribute: nil,
                                             errorCode: Int(timeoutError.rawValue), valueTypeID: nil))
        }
        var count: CFIndex = 0
        let countError = AXUIElementGetAttributeValueCount(appElement, kAXWindowsAttribute as CFString, &count)
        guard permitted(), countError == .success, count > 0, count <= maxWindows else {
            return failure("ax_window_inventory_failed", axTrusted: axTrusted,
                           read: ReadFailure(stage: permitted() ? "application.windows.count" : "permission.gate",
                                             attribute: "AXWindows", errorCode: countError == .success ? nil : Int(countError.rawValue),
                                             valueTypeID: nil))
        }
        var value: CFArray?
        let valuesError = AXUIElementCopyAttributeValues(appElement, kAXWindowsAttribute as CFString, 0, count, &value)
        guard permitted(), valuesError == .success, let elements = value as? [AXUIElement] else {
            return failure("ax_window_values_failed", axTrusted: axTrusted, axWindowCount: Int(count),
                           read: ReadFailure(stage: permitted() ? "application.windows.values" : "permission.gate",
                                             attribute: "AXWindows", errorCode: valuesError == .success ? nil : Int(valuesError.rawValue),
                                             valueTypeID: value.map { UInt64(CFGetTypeID($0)) }))
        }
        // Do not drop unreadable windows: that could hide an ambiguous match.
        // Timeout is per AX object (not inherited from its application).
        var candidates: [(AXUIElement, AccessibleWindowDescriptor)] = []
        for element in elements {
            var pid: pid_t = 0
            let pidError = AXUIElementGetPid(element, &pid)
            guard permitted(), pidError == .success else {
                return failure("ax_window_unreadable", axTrusted: axTrusted, axWindowCount: Int(count),
                               read: ReadFailure(stage: permitted() ? "window.pid" : "permission.gate",
                                                 attribute: nil, errorCode: pidError == .success ? nil : Int(pidError.rawValue),
                                                 valueTypeID: nil))
            }
            guard pid == target.ownerPID else {
                return failure("ax_window_unreadable", axTrusted: axTrusted, axWindowCount: Int(count),
                               read: ReadFailure(stage: "window.pid.mismatch", attribute: nil,
                                                 errorCode: nil, valueTypeID: nil))
            }
            let windowTimeoutError = AXUIElementSetMessagingTimeout(element, messagingTimeout)
            guard permitted(), windowTimeoutError == .success else {
                return failure("ax_window_unreadable", axTrusted: axTrusted, axWindowCount: Int(count),
                               read: ReadFailure(stage: permitted() ? "window.timeout" : "permission.gate",
                                                 attribute: nil, errorCode: windowTimeoutError == .success ? nil : Int(windowTimeoutError.rawValue),
                                                 valueTypeID: nil))
            }
            switch descriptor(element, permitted: permitted) {
            case .success(let descriptor):
                candidates.append((element, descriptor))
            case .failure(let read):
                return failure("ax_window_unreadable", axTrusted: axTrusted, axWindowCount: Int(count), read: read)
            }
        }
        let matches = WindowTargetSelector.matchingIndices(for: target, candidates: candidates.map { $0.1 })
        guard matches.count == 1 else {
            return failure(matches.isEmpty ? "ax_geometry_no_match" : "ax_geometry_ambiguous",
                           axTrusted: axTrusted, axWindowCount: Int(count), geometryMatchCount: matches.count)
        }
        return Inspection(result: AccessibilityWindowActivationResult(success: true, reason: "", axTrusted: axTrusted,
                                                                       axWindowCount: Int(count), geometryMatchCount: matches.count),
                          element: candidates[matches[0]].0)
    }

    private static func attribute(_ element: AXUIElement, _ name: String, permitted: () -> Bool) -> AttributeRead {
        guard permitted() else { return AttributeRead(value: nil, errorCode: nil, permissionRevoked: true) }
        var value: CFTypeRef?
        let error = AXUIElementCopyAttributeValue(element, name as CFString, &value)
        return AttributeRead(value: value, errorCode: error == .success ? nil : Int(error.rawValue), permissionRevoked: false)
    }

    private static func descriptor(_ element: AXUIElement, permitted: () -> Bool) -> Result<AccessibleWindowDescriptor, ReadFailure> {
        let position = attribute(element, kAXPositionAttribute, permitted: permitted)
        guard !position.permissionRevoked else {
            return .failure(ReadFailure(stage: "window.position.permission", attribute: "AXPosition", errorCode: nil, valueTypeID: nil))
        }
        guard let positionValue = position.value, position.errorCode == nil else {
            return .failure(ReadFailure(stage: "window.position.read", attribute: "AXPosition",
                                         errorCode: position.errorCode, valueTypeID: position.value.map { UInt64(CFGetTypeID($0)) }))
        }
        guard CFGetTypeID(positionValue) == AXValueGetTypeID() else {
            return .failure(ReadFailure(stage: "window.position.type", attribute: "AXPosition", errorCode: nil,
                                         valueTypeID: UInt64(CFGetTypeID(positionValue))))
        }

        let size = attribute(element, kAXSizeAttribute, permitted: permitted)
        guard !size.permissionRevoked else {
            return .failure(ReadFailure(stage: "window.size.permission", attribute: "AXSize", errorCode: nil, valueTypeID: nil))
        }
        guard let sizeValue = size.value, size.errorCode == nil else {
            return .failure(ReadFailure(stage: "window.size.read", attribute: "AXSize",
                                         errorCode: size.errorCode, valueTypeID: size.value.map { UInt64(CFGetTypeID($0)) }))
        }
        guard CFGetTypeID(sizeValue) == AXValueGetTypeID() else {
            return .failure(ReadFailure(stage: "window.size.type", attribute: "AXSize", errorCode: nil,
                                         valueTypeID: UInt64(CFGetTypeID(sizeValue))))
        }

        let minimized = attribute(element, kAXMinimizedAttribute, permitted: permitted)
        guard !minimized.permissionRevoked else {
            return .failure(ReadFailure(stage: "window.minimized.permission", attribute: "AXMinimized", errorCode: nil, valueTypeID: nil))
        }
        guard let minimizedValue = minimized.value, minimized.errorCode == nil,
              let minimizedBool = minimizedValue as? Bool else {
            return .failure(ReadFailure(stage: "window.minimized.read", attribute: "AXMinimized",
                                         errorCode: minimized.errorCode, valueTypeID: minimized.value.map { UInt64(CFGetTypeID($0)) }))
        }

        var point = CGPoint.zero
        var dimensions = CGSize.zero
        guard AXValueGetValue(positionValue as! AXValue, .cgPoint, &point) else {
            return .failure(ReadFailure(stage: "window.position.value", attribute: "AXPosition", errorCode: nil,
                                         valueTypeID: UInt64(CFGetTypeID(positionValue))))
        }
        guard AXValueGetValue(sizeValue as! AXValue, .cgSize, &dimensions) else {
            return .failure(ReadFailure(stage: "window.size.value", attribute: "AXSize", errorCode: nil,
                                         valueTypeID: UInt64(CFGetTypeID(sizeValue))))
        }
        let title = attribute(element, kAXTitleAttribute, permitted: permitted).value as? String ?? ""
        return .success(AccessibleWindowDescriptor(frame: CGRect(origin: point, size: dimensions),
                                                    title: title, minimized: minimizedBool))
    }

}
