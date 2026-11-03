import Foundation
import CoreGraphics
import Darwin

// Guarded input uses this private inherited channel. The host owns complete
// input batches and drag posting/release, and survives helper death. No named
// socket or local guarded-input fallback is allowed when the channel fails.
protocol MouseBrokerWire {
    func exchange(_ request: Data) throws -> Data
}

private enum BrokerLimits {
    static let frameBytes = 4096
    static let timeoutSeconds: TimeInterval = 0.1
    // Opt-in validation-only hook. It is inert unless a local acceptance run
    // explicitly sets this environment variable; production builds never set it.
    static let testBatchHoldEnvironment = "NIUMA_TEST_HELPER_HOLD_AFTER_BATCH_MS"
    static let maxTestBatchHoldMS = 60_000
}

final class PipeMouseBrokerWire: MouseBrokerWire {
    private let requestFD: Int32
    private let responseFD: Int32

    init(requestFD: Int32 = 3, responseFD: Int32 = 4) throws {
        self.requestFD = requestFD
        self.responseFD = responseFD
        for fd in [requestFD, responseFD] {
            let flags = fcntl(fd, F_GETFL)
            guard flags >= 0, fcntl(fd, F_SETFL, flags | O_NONBLOCK) == 0 else {
                throw SafetyError.inputUnavailable
            }
        }
        signal(SIGPIPE, SIG_IGN)
    }

    private func ready(_ fd: Int32, events: Int16, deadline: TimeInterval) throws {
        while true {
            let remaining = deadline - ProcessInfo.processInfo.systemUptime
            guard remaining > 0 else { throw SafetyError.inputUncertain }
            var descriptor = pollfd(fd: fd, events: events, revents: 0)
            let status = poll(&descriptor, 1, Int32(ceil(remaining * 1000)))
            if status < 0 && errno == EINTR { continue }
            guard status > 0, descriptor.revents & events != 0 else { throw SafetyError.inputUncertain }
            return
        }
    }

    private func holdForValidationIfRequested(_ request: Data) {
        guard request.range(of: Data("\"phase\":\"batch\"".utf8)) != nil,
              let raw = ProcessInfo.processInfo.environment[BrokerLimits.testBatchHoldEnvironment],
              let milliseconds = Int(raw), milliseconds > 0,
              milliseconds <= BrokerLimits.maxTestBatchHoldMS else { return }
        usleep(useconds_t(milliseconds * 1000))
    }

    func exchange(_ request: Data) throws -> Data {
        guard request.count < BrokerLimits.frameBytes else { throw SafetyError.inputUnavailable }
        let deadline = ProcessInfo.processInfo.systemUptime + BrokerLimits.timeoutSeconds
        var frame = request
        frame.append(0x0a)
        try frame.withUnsafeBytes { bytes in
            var offset = 0
            while offset < bytes.count {
                try ready(requestFD, events: Int16(POLLOUT), deadline: deadline)
                let count = Darwin.write(requestFD, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
                if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
                guard count > 0 else { throw SafetyError.inputUncertain }
                offset += count
            }
        }
        // This occurs after the host has received and committed the bounded
        // batch request, but before the helper reads its ACK. A validation run
        // can SIGKILL the helper here to prove the host-owned batch is balanced
        // and the caller reports unknown rather than replaying input.
        holdForValidationIfRequested(request)
        var result = Data()
        while result.count < BrokerLimits.frameBytes {
            try ready(responseFD, events: Int16(POLLIN), deadline: deadline)
            var byte: UInt8 = 0
            let count = Darwin.read(responseFD, &byte, 1)
            if count < 0 && (errno == EINTR || errno == EAGAIN) { continue }
            guard count == 1 else { throw SafetyError.inputUncertain }
            if byte == 0x0a { return result }
            result.append(byte)
        }
        throw SafetyError.inputUncertain
    }
}

final class MouseButtonClient {
    static let batchCountRange = 1...4096
    private static let maxTokenLength = 128
    private struct BatchRequest: Encodable {
        let token: String
        let sequence: Int
        let phase = "batch"
    }
    private enum Phase: String, Encodable { case down, drag, up }
    private struct Request: Encodable {
        let token: String
        let sequence: Int
        let phase: Phase
        let button: String
        let x: Double
        let y: Double
    }
    private struct Response: Decodable {
        let sequence: Int
        let ok: Bool
        let error_code: String?
    }
    private let wire: MouseBrokerWire
    private var token = ""
    private var sequence = 0
    private var batchCount = 0
    private var batchUncertain = false
    private var held: CGMouseButton?
    private var lastPoint = CGPoint.zero

    init(wire: MouseBrokerWire) { self.wire = wire }

    func prepare(token: String) throws {
        guard !batchUncertain, held == nil, !token.isEmpty, token.count <= Self.maxTokenLength else { throw SafetyError.inputUnavailable }
        self.token = token
        sequence = 0
        batchCount = 0
    }

    func prepareBatch(token: String, count: Int) throws {
        guard !batchUncertain, held == nil else { throw SafetyError.inputUnavailable }
        // Failed preparation must not leave an older authorization usable.
        self.token = ""
        batchCount = 0
        guard !token.isEmpty, token.count <= Self.maxTokenLength,
              Self.batchCountRange.contains(count) else { throw SafetyError.inputUnavailable }
        self.token = token
        sequence = 0
        batchCount = count
    }

    func batch() throws {
        guard !batchUncertain, held == nil, !token.isEmpty,
              sequence < batchCount else { throw SafetyError.inputUnavailable }
        sequence += 1
        // No event data crosses this channel: only advance the host-owned plan.
        let request = try JSONEncoder().encode(BatchRequest(token: token, sequence: sequence))
        let response: Response
        do {
            let reply = try wire.exchange(request)
            guard let decoded = try? JSONDecoder().decode(Response.self, from: reply),
                  decoded.sequence == sequence else { throw SafetyError.inputUncertain }
            response = decoded
        } catch {
            // A delayed ACK may remain on the shared FD. Never retry or prepare
            // another action on this client after an indeterminate exchange.
            batchUncertain = true
            token = ""
            throw SafetyError.inputUncertain
        }
        guard response.ok else {
            token = ""
            throw response.error_code == SafetyError.inactive.rawValue ? SafetyError.inactive : SafetyError.inputUnavailable
        }
    }

    private func send(_ phase: Phase, point: CGPoint, button: CGMouseButton) throws {
        guard !batchUncertain, batchCount == 0, !token.isEmpty, point.x.isFinite, point.y.isFinite,
              button == .left || button == .right else { throw SafetyError.inputUnavailable }
        sequence += 1
        let request = Request(token: token, sequence: sequence, phase: phase,
                              button: button == .right ? "right" : "left", x: point.x, y: point.y)
        let reply = try wire.exchange(JSONEncoder().encode(request))
        guard let response = try? JSONDecoder().decode(Response.self, from: reply),
              response.sequence == sequence else { throw SafetyError.inputUncertain }
        guard response.ok else {
            throw response.error_code == SafetyError.inactive.rawValue ? SafetyError.inactive : SafetyError.inputUnavailable
        }
    }

    func down(point: CGPoint, button: CGMouseButton) throws {
        guard held == nil else { throw SafetyError.inputUnavailable }
        // Track before sending: the host may post down then lose the ACK.
        held = button
        lastPoint = point
        try send(.down, point: point, button: button)
    }
    func drag(point: CGPoint, button: CGMouseButton) throws {
        guard held == button else { throw SafetyError.inputUnavailable }
        try send(.drag, point: point, button: button)
        lastPoint = point
    }
    func up(point: CGPoint, button: CGMouseButton) throws {
        guard held == button else { throw SafetyError.inputUnavailable }
        try send(.up, point: point, button: button)
        held = nil
        token = ""
    }
    func release(button: CGMouseButton) {
        guard held == button else { return }
        // Host release is idempotent and owns the actual last-posted point.
        // If this exchange fails, host action/process cleanup still releases.
        try? send(.up, point: lastPoint, button: button)
        held = nil
        token = ""
    }
}
