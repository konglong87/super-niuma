import Foundation

let protocolVersion = "computer-use.v1"
let maxFrameBytes = 8 * 1024 * 1024
let maxRequestSeconds: TimeInterval = 60
// Observation freshness is distinct from the deadline of the capture RPC.
// Must match computeruse.DefaultObservationTTL; the host may shorten it.
let observationTTLSeconds: TimeInterval = 120
let maxRememberedIDs = 4096
let maxImagePixels = 32 * 1024 * 1024
let maxImageDimension = 16384
let maxPNGBytes = 5 * 1024 * 1024
let maxTextUnits = 4096
let maxWaitMS = 10000
let maxScrollDelta = 10000
let appLaunchTimeoutSeconds: TimeInterval = 8
let appLaunchPollMS = 50

enum Command: String { case requestPermissions = "request_permissions", readiness, launchApp = "launch_app", observe, execute, pause, resume, stop, shutdown }
enum Outcome: String { case executed, rejected, unknown, failed }
enum DispatchState: String { case notStarted = "not_started", complete, partial, unknown }
enum ActionKind: String, CaseIterable {
    case click, doubleClick = "double_click", rightClick = "right_click", move, drag, type, key, hotkey, scroll, wait
}

struct Envelope: Codable {
    let protocolVersion: String
    let requestID: String
    let sessionID: String
    let actionID: String
    let deadline: String
    let command: String
    let payload: JSONValue
    enum CodingKeys: String, CodingKey {
        case protocolVersion = "protocol_version", requestID = "request_id", sessionID = "session_id", actionID = "action_id"
        case deadline, command, payload
    }
    var expires: Date? {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = f.date(from: deadline) { return date }
        f.formatOptions = [.withInternetDateTime]
        return f.date(from: deadline)
    }
    func valid(at now: Date) -> Bool {
        guard protocolVersion == "computer-use.v1", !requestID.isEmpty, !sessionID.isEmpty, !actionID.isEmpty,
              requestID.utf8.count <= 256, sessionID.utf8.count <= 256, actionID.utf8.count <= 256,
              let date = expires else { return false }
        return date > now && date.timeIntervalSince(now) <= maxRequestSeconds
    }
}

enum JSONValue: Codable {
    case object([String: JSONValue]), array([JSONValue]), string(String), number(Double), bool(Bool), null
    init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null }
        else if let v = try? c.decode(Bool.self) { self = .bool(v) }
        else if let v = try? c.decode(String.self) { self = .string(v) }
        else if let v = try? c.decode(Double.self) { self = .number(v) }
        else if let v = try? c.decode([String: JSONValue].self) { self = .object(v) }
        else if let v = try? c.decode([JSONValue].self) { self = .array(v) }
        else { throw SafetyError.invalidAction }
    }
    func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .object(let v): try c.encode(v)
        case .array(let v): try c.encode(v)
        case .string(let v): try c.encode(v)
        case .number(let v): try c.encode(v)
        case .bool(let v): try c.encode(v)
        case .null: try c.encodeNil()
        }
    }
    subscript(_ key: String) -> JSONValue? { if case .object(let o) = self { return o[key] }; return nil }
    var string: String? { if case .string(let v) = self { return v }; return nil }
    var bool: Bool? { if case .bool(let v) = self { return v }; return nil }
    var array: [JSONValue]? { if case .array(let v) = self { return v }; return nil }
    func integer(in range: ClosedRange<Int>) -> Int? {
        guard case .number(let n) = self, n.isFinite, n.rounded(.towardZero) == n,
              n >= Double(range.lowerBound), n <= Double(range.upperBound), let value = Int(exactly: n), range.contains(value) else { return nil }
        return value
    }
}

enum SafetyError: String, Error {
    case invalidAction = "invalid_action", invalidEnvelope = "invalid_envelope", inactive, expired, staleObservation = "stale_observation"
    case permissionRequired = "permission_required", focusChanged = "focus_changed", unsupportedDisplay = "unsupported_display", targetWindowMismatch = "target_window_mismatch", targetExitedAfterLaunch = "target_exited_after_launch"
    case unsupportedApplication = "unsupported_application", launchFailed = "launch_failed", launchTimeout = "launch_timeout"
    case screenshotFailed = "screenshot_failed", inputUnavailable = "input_unavailable", inputUncertain = "input_uncertain", duplicate, capacity
}

// Origin is retained separately from the fact that application-open/activation
// dispatch already occurred. A late timeout must never mean not_started.
struct LaunchOperationError: Error {
    let origin: SafetyError
}

// FileHandle reads may be short; prefix and body both require read-exactly.
func readExactly(_ count: Int, from handle: FileHandle) -> Data? {
    var result = Data()
    while result.count < count {
        guard let part = try? handle.read(upToCount: count - result.count), !part.isEmpty else { return nil }
        result.append(part)
    }
    return result
}
func readFrame() -> Data? {
    guard let prefix = readExactly(4, from: .standardInput) else { return nil }
    let size = prefix.reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
    guard size > 0 && size <= maxFrameBytes else { return nil }
    return readExactly(Int(size), from: .standardInput)
}

final class Responder {
    private let lock = NSLock()
    func send(_ request: Envelope, outcome: Outcome, result: JSONValue = .object([:]), error: SafetyError? = nil, dispatchState: DispatchState? = nil) {
        var payload: [String: JSONValue] = ["ok": .bool(outcome == .executed), "outcome": .string(outcome.rawValue), "result": result]
        if let error { payload["error_code"] = .string(error.rawValue) }
        if let dispatchState { payload["dispatch_state"] = .string(dispatchState.rawValue) }
        let response = Envelope(protocolVersion: protocolVersion, requestID: request.requestID, sessionID: request.sessionID,
                                actionID: request.actionID, deadline: request.deadline, command: request.command, payload: .object(payload))
        guard let body = try? JSONEncoder().encode(response), body.count <= maxFrameBytes else { exit(1) }
        var size = UInt32(body.count).bigEndian
        var frame = withUnsafeBytes(of: &size) { Data($0) }; frame.append(body)
        lock.lock(); defer { lock.unlock() }
        do { try FileHandle.standardOutput.write(contentsOf: frame) } catch { exit(1) }
    }
}
