import Foundation
import CoreGraphics
import Carbon.HIToolbox
import Darwin

private final class FakeMouseBrokerWire: MouseBrokerWire {
    var messages: [[String: Any]] = []
    var badSequence = false
    var inactive = false
    var lostAck = false
    func exchange(_ request: Data) throws -> Data {
        let payload = try JSONSerialization.jsonObject(with: request) as! [String: Any]
        messages.append(payload)
        if lostAck { throw SafetyError.inputUncertain }
        return try JSONSerialization.data(withJSONObject: [
            "sequence": (payload["sequence"] as! Int) + (badSequence ? 1 : 0),
            "ok": !inactive, "error_code": inactive ? "inactive" : "",
        ])
    }
}

// Constructs events only. Never posts input, prompts for TCC, or captures UI.
@main
struct PlatformTests {
    static var assertions = 0
    static func expect(_ condition: @autoclosure () -> Bool, _ label: String) {
        assertions += 1
        guard condition() else { fatalError("FAIL: \(label)") }
    }
    static func check(_ event: CGEvent, _ type: CGEventType, _ key: Int,
                      _ flags: CGEventFlags, _ label: String) {
        expect(event.type == type, "\(label): event type")
        expect(event.getIntegerValueField(.keyboardEventKeycode) == Int64(key), "\(label): keycode")
        expect(event.flags == flags, "\(label): flags")
    }
    static func batchChecks() throws {
        let wire = FakeMouseBrokerWire()
        let client = MouseButtonClient(wire: wire)
        try client.prepareBatch(token: "batch-token", count: 4)
        for _ in 0..<4 { try client.batch() }
        expect(wire.messages.count == 4, "batch count")
        expect(wire.messages.compactMap { $0["phase"] as? String } == ["batch", "batch", "batch", "batch"], "batch phase")
        expect(wire.messages.compactMap { $0["sequence"] as? Int } == [1, 2, 3, 4], "batch sequence")
        expect(wire.messages.allSatisfy { $0["token"] as? String == "batch-token" && $0["button"] == nil && $0["x"] == nil && $0["y"] == nil }, "batch carries no event data")
        do { try client.batch(); expect(false, "batch overrun rejected") } catch SafetyError.inputUnavailable { }

        let inactiveWire = FakeMouseBrokerWire()
        let inactive = MouseButtonClient(wire: inactiveWire)
        try inactive.prepareBatch(token: "inactive-token", count: 1)
        inactiveWire.inactive = true
        do { try inactive.batch(); expect(false, "inactive batch accepted") } catch SafetyError.inactive { }

        let uncertainWire = FakeMouseBrokerWire()
        let uncertain = MouseButtonClient(wire: uncertainWire)
        try uncertain.prepareBatch(token: "uncertain-token", count: 1)
        uncertainWire.lostAck = true
        do { try uncertain.batch(); expect(false, "lost ACK accepted") } catch SafetyError.inputUncertain { }
        do { try uncertain.prepareBatch(token: "retry-token", count: 1); expect(false, "uncertain client reused") } catch SafetyError.inputUnavailable { }
    }

    static func brokerPipeChecks() throws {
        var outgoing: [Int32] = [0, 0]
        var incoming: [Int32] = [0, 0]
        guard pipe(&outgoing) == 0, pipe(&incoming) == 0 else { fatalError("pipe setup") }
        defer { for fd in outgoing + incoming { Darwin.close(fd) } }
        let wire = try PipeMouseBrokerWire(requestFD: outgoing[1], responseFD: incoming[0])
        let reply = Data("{\"sequence\":1,\"ok\":true}\n".utf8)
        _ = reply.withUnsafeBytes { Darwin.write(incoming[1], $0.baseAddress, $0.count) }
        let request = Data("{\"token\":\"test\"}".utf8)
        let actualReply = try wire.exchange(request)
        expect(actualReply == reply.dropLast(), "inherited pipe framing round-trip")
        var bytes = [UInt8](repeating: 0, count: 256)
        let size = Darwin.read(outgoing[0], &bytes, bytes.count)
        expect(Data(bytes.prefix(size)) == request + Data([0x0a]), "one bounded newline request")
        let start = ProcessInfo.processInfo.systemUptime
        do {
            _ = try wire.exchange(request)
            expect(false, "missing ACK must timeout")
        } catch SafetyError.inputUncertain {
            expect(ProcessInfo.processInfo.systemUptime - start < 0.5, "IPC timeout bounded below Stop grace")
        }
    }
    static func brokerChecks() throws {
        let wire = FakeMouseBrokerWire()
        let client = MouseButtonClient(wire: wire)
        for button in [CGMouseButton.left, .right] {
            try client.prepare(token: "fixture-token")
            try client.down(point: CGPoint(x: -10, y: 20), button: button)
            try client.drag(point: CGPoint(x: -5, y: 30), button: button)
            try client.up(point: CGPoint(x: -5, y: 30), button: button)
            let count = wire.messages.count
            client.release(button: button)
            expect(wire.messages.count == count, "acknowledged up needs no cleanup resend")
            let triplet = Array(wire.messages.suffix(3))
            expect(triplet.compactMap { $0["phase"] as? String } == ["down", "drag", "up"], "broker event order")
            expect(triplet.compactMap { $0["sequence"] as? Int } == [1, 2, 3], "lease sequence starts fresh")
            expect(triplet.allSatisfy { $0["button"] as? String == (button == .right ? "right" : "left") }, "button binding")
            expect(triplet.last?["x"] as? Double == -5, "CG negative origin not flipped")
        }
        for mode in ["lost", "binding", "inactive"] {
            try client.prepare(token: "failure-token")
            wire.lostAck = mode == "lost"
            wire.badSequence = mode == "binding"
            wire.inactive = mode == "inactive"
            do {
                try client.down(point: .zero, button: .left)
                expect(false, "bad down reply fails")
            } catch let error as SafetyError {
                expect(error == (mode == "inactive" ? .inactive : .inputUncertain), "uncertain ACK or revoked lease classification")
            }
            wire.lostAck = false; wire.badSequence = false; wire.inactive = false
            client.release(button: .left)
            expect(wire.messages.last?["phase"] as? String == "up", "lost down ACK still requests release")
        }
        expect(!MacDesktop().supportsDrag(), "no broker means no unguarded drag capability")
    }
    static func main() throws {
        try brokerChecks()
        try batchChecks()
        try brokerPipeChecks()
        let commandTab = try KeyboardEventSequence.make(code: CGKeyCode(kVK_Tab), flags: .maskCommand)
        expect(commandTab.count == 4, "command-tab has explicit modifier press/release")
        check(commandTab[0], .flagsChanged, kVK_Command, .maskCommand, "command down")
        check(commandTab[1], .keyDown, kVK_Tab, .maskCommand, "tab down")
        check(commandTab[2], .keyUp, kVK_Tab, .maskCommand, "tab up while command held")
        check(commandTab[3], .flagsChanged, kVK_Command, [], "command released")

        // All 16 combinations, including plain key input. This checks each
        // supported modifier, cumulative flags, reverse release, and final []
        // without depending on the physical keyboard's current modifier state.
        let modifiers: [(Int, CGEventFlags)] = [
            (kVK_Control, .maskControl), (kVK_Option, .maskAlternate),
            (kVK_Shift, .maskShift), (kVK_Command, .maskCommand),
        ]
        for mask in 0..<(1 << modifiers.count) {
            let selected = modifiers.enumerated().filter { mask & (1 << $0.offset) != 0 }.map { $0.element }
            let flags = selected.reduce(CGEventFlags()) { $0.union($1.1) }
            let events = try KeyboardEventSequence.make(code: CGKeyCode(kVK_Tab), flags: flags)
            expect(events.count == selected.count * 2 + 2, "balanced sequence")
            var held: CGEventFlags = []
            var index = 0
            for (key, flag) in selected {
                held.insert(flag)
                check(events[index], .flagsChanged, key, held, "modifier down")
                index += 1
            }
            check(events[index], .keyDown, kVK_Tab, flags, "main down")
            check(events[index + 1], .keyUp, kVK_Tab, flags, "main up")
            index += 2
            for (key, flag) in selected.reversed() {
                held.remove(flag)
                check(events[index], .flagsChanged, key, held, "modifier up")
                index += 1
            }
            expect(events.last!.flags.isEmpty, "no modifier remains")

            // Fail every allocation position, especially modifier releases.
            // Construction must throw, never return a partially postable chord.
            for failure in events.indices {
                var allocations = 0
                do {
                    _ = try KeyboardEventSequence.make(code: CGKeyCode(kVK_Tab), flags: flags) { key, down in
                        defer { allocations += 1 }
                        if allocations == failure { return nil }
                        return CGEvent(keyboardEventSource: nil, virtualKey: key, keyDown: down)
                    }
                    expect(false, "allocation failure must throw")
                } catch SafetyError.inputUnavailable {
                    expect(allocations == failure + 1, "stop at failed allocation")
                }
            }
        }
        let escape = try KeyboardEventSequence.make(code: CGKeyCode(kVK_Escape), flags: [])
        expect(escape.count == 2, "unmodified escape remains a key pair")
        check(escape[0], .keyDown, kVK_Escape, [], "escape down")
        check(escape[1], .keyUp, kVK_Escape, [], "escape up")
        for kind in [ActionKind.click, .doubleClick, .rightClick] {
            let events = try MouseEventSequence.make(kind: kind, point: CGPoint(x: 10, y: 20), clickCount: 2)
            expect(events.count == 2, "mouse pair preallocated")
            expect(events[1].getIntegerValueField(.mouseEventClickState) == 2, "click count preserved")
            var allocation = 0
            do {
                _ = try MouseEventSequence.make(kind: kind, point: .zero, clickCount: 1) { type, point, button in
                    allocation += 1
                    return allocation == 2 ? nil : CGEvent(mouseEventSource: nil, mouseType: type, mouseCursorPosition: point, mouseButton: button)
                }
                expect(false, "up allocation failure must not return postable down")
            } catch SafetyError.inputUnavailable { expect(allocation == 2, "mouse construction stops on failed release") }
        }
        print("PASS: \(assertions) platform event assertions (no events posted)")
    }
}
