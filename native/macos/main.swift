import Foundation

// Reader/control stays independent of the serial desktop executor. Bounded
// outstanding work avoids retaining arbitrary action/text queues in memory.
let brokerWire = CommandLine.arguments.contains("--mouse-broker") ? try? PipeMouseBrokerWire() : nil
let mouseBroker = brokerWire.map { MouseButtonClient(wire: $0) }
let engine = Engine(platform: MacDesktop(mouseBroker: mouseBroker, diagnostic: NativeDiagnostics.append), diagnostic: NativeDiagnostics.append)
let responder = Responder()
let executor = DispatchQueue(label: "computer-helper.actions")
let slots = DispatchSemaphore(value: 2)
while let data = readFrame() {
    guard let request = try? JSONDecoder().decode(Envelope.self, from: data) else { break }
    do {
        try engine.state.register(request)
        guard let command = Command(rawValue: request.command) else { throw SafetyError.invalidEnvelope }
        switch command {
        case .requestPermissions:
            responder.send(request, outcome: .executed, result: engine.requestPermissions())
        case .pause, .resume, .stop, .shutdown:
            let generation = request.payload["generation"]?.integer(in: 0...Int(Int32.max)) ?? -1
            try engine.state.control(command, generation: generation)
            // Invalidate immediately, but acknowledge only after queued input
            // exits and its mouse-up defer has run. Never block the reader.
            executor.async {
                responder.send(request, outcome: .executed)
                if command == .shutdown { exit(0) }
            }
        case .readiness: responder.send(request, outcome: .executed, result: engine.readiness())
        case .launchApp, .observe, .execute:
            guard slots.wait(timeout: .now()) == .success else { throw SafetyError.capacity }
            executor.async {
                defer { slots.signal() }
                let result: ActionResult
                switch command {
                case .launchApp: result = engine.launchApp(request)
                case .observe: result = engine.observe(request)
                case .execute: result = engine.execute(request)
                default: result = ActionResult(outcome: .rejected, payload: .object([:]), error: .invalidEnvelope)
                }
                responder.send(request, outcome: result.outcome, result: result.payload, error: result.error, dispatchState: result.dispatchState)
            }
        }
    } catch { responder.send(request, outcome: .rejected, error: error as? SafetyError ?? .invalidEnvelope) }
}
engine.state.end()
exit(0)
