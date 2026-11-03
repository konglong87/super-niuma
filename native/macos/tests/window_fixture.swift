import AppKit

// Build: swiftc -swift-version 6 -warnings-as-errors -parse-as-library -framework AppKit window_fixture.swift -o window-fixture
// Run: window-fixture --output /absolute/path/state.json
// JSON geometry is in global CG top-left points (not backing pixels). Window
// width/height include the title bar; buttonScreenFrame is the NSButton bounds.
// frontmost means the application's foremost visible window while it is active;
// keyWindow is AppKit's isKeyWindow. Closed windows disappear from the array.
private enum Fixture {
    static let title = "Computer Target Fixture"
    static let refreshInterval: TimeInterval = 0.25
    static let minimumSize = NSSize(width: 360, height: 440)
    static let dragAreaSize = NSSize(width: 300, height: 112)
    static let maximumDragEvents = 512
    static let keys = ["A", "B"]
    static let geometryStep: CGFloat = 20
}

private struct ScreenFrame: Encodable {
    let x: CGFloat
    let y: CGFloat
    let width: CGFloat
    let height: CGFloat

    init(_ appKitFrame: NSRect, primaryTop: CGFloat) {
        x = appKitFrame.minX
        y = primaryTop - appKitFrame.maxY
        width = appKitFrame.width
        height = appKitFrame.height
    }
}

private enum MouseButton: String, Encodable {
    case left, right
}

private enum MousePhase: String, Encodable {
    case down = "mouseDown"
    case dragged = "mouseDragged"
    case up = "mouseUp"
}

private struct MouseEvent: Encodable {
    let seq: Int
    let type: MousePhase
    let button: MouseButton
    // Global CG top-left points, just like the snapshot's screen frames.
    let x: CGFloat
    let y: CGFloat
    let timestamp: TimeInterval
    let pressed: Bool
}

// This is deliberately NOT an NSDraggingSession: there is no pasteboard, file
// drop, context menu, global monitor, or effect outside this fixture. AppKit
// delivers the drag/up sequence to the view that received the initial down.
@MainActor
private final class DragAreaView: NSView {
    private(set) var dragEvents: [MouseEvent] = []
    private(set) var lastMouseEvent: MouseEvent?
    private(set) var dropCompleted = false
    private var pressedButtons: Set<MouseButton> = []
    private var startedOnLeft: Set<MouseButton> = []
    private var sequence = 0
    var onEvent: (() -> Void)?

    var pressed: Bool { !pressedButtons.isEmpty }

    override func acceptsFirstMouse(for event: NSEvent?) -> Bool { true }

    override func draw(_ dirtyRect: NSRect) {
        (pressed ? NSColor.systemOrange.withAlphaComponent(0.2) :
            (dropCompleted ? NSColor.systemGreen.withAlphaComponent(0.15) : NSColor.controlBackgroundColor)).setFill()
        bounds.fill()
        NSColor.separatorColor.setStroke()
        let border = NSBezierPath(rect: bounds.insetBy(dx: 1, dy: 1))
        border.move(to: NSPoint(x: bounds.midX, y: bounds.minY))
        border.line(to: NSPoint(x: bounds.midX, y: bounds.maxY))
        border.stroke()
        let attributes: [NSAttributedString.Key: Any] = [
            .font: NSFont.systemFont(ofSize: 15, weight: .medium),
            .foregroundColor: NSColor.labelColor,
        ]
        ("Drag start" as NSString).draw(at: NSPoint(x: 20, y: bounds.midY), withAttributes: attributes)
        ("Release here" as NSString).draw(at: NSPoint(x: bounds.midX + 20, y: bounds.midY),
                                        withAttributes: attributes)
        let status = pressed ? "Holding mouse button" :
            (dropCompleted ? "Released — drop complete" : (lastMouseEvent?.type == .up ? "Released — interrupted" : "Ready"))
        (status as NSString).draw(at: NSPoint(x: 20, y: 12), withAttributes: [
            .font: NSFont.systemFont(ofSize: 13, weight: .semibold),
            .foregroundColor: NSColor.labelColor,
        ])
    }

    override func mouseDown(with event: NSEvent) { record(event, phase: .down, button: .left) }
    override func mouseDragged(with event: NSEvent) { record(event, phase: .dragged, button: .left) }
    override func mouseUp(with event: NSEvent) { record(event, phase: .up, button: .left) }
    override func rightMouseDown(with event: NSEvent) { record(event, phase: .down, button: .right) }
    override func rightMouseDragged(with event: NSEvent) { record(event, phase: .dragged, button: .right) }
    override func rightMouseUp(with event: NSEvent) { record(event, phase: .up, button: .right) }

    private func record(_ event: NSEvent, phase: MousePhase, button: MouseButton) {
        guard let window, let primaryScreen = NSScreen.screens.first else { return }
        let local = convert(event.locationInWindow, from: nil)
        let screen = window.convertPoint(toScreen: event.locationInWindow)
        switch phase {
        case .down:
            pressedButtons.insert(button)
            startedOnLeft.remove(button)
            if bounds.contains(local), local.x < bounds.midX { startedOnLeft.insert(button) }
            dropCompleted = false
        case .dragged:
            break
        case .up:
            dropCompleted = pressedButtons.contains(button) && startedOnLeft.contains(button)
                && bounds.contains(local) && local.x >= bounds.midX
            pressedButtons.remove(button)
            startedOnLeft.remove(button)
        }
        // Never reset pressed on deactivation or a timer: only an actual up
        // clears it, so interrupted input cannot falsely appear released.
        sequence += 1
        let recorded = MouseEvent(seq: sequence, type: phase, button: button,
                                  x: screen.x, y: primaryScreen.frame.maxY - screen.y,
                                  timestamp: event.timestamp, pressed: pressed)
        lastMouseEvent = recorded
        dragEvents.append(recorded)
        if dragEvents.count > Fixture.maximumDragEvents {
            dragEvents.removeFirst(dragEvents.count - Fixture.maximumDragEvents)
        }
        needsDisplay = true
        onEvent?()
    }
}

private struct WindowState: Encodable {
    let key: String
    let windowNumber: Int
    let width: CGFloat
    let height: CGFloat
    let clickCount: Int
    let frame: ScreenFrame
    let buttonScreenFrame: ScreenFrame
    let dragAreaScreenFrame: ScreenFrame
    let dragEvents: [MouseEvent]
    let lastMouseEvent: MouseEvent?
    let pressed: Bool
    let dropCompleted: Bool
    let frontmost: Bool
    let keyWindow: Bool
    let minimized: Bool
    let visible: Bool

    private enum CodingKeys: String, CodingKey {
        case key, windowNumber, width, height, clickCount, frame, buttonScreenFrame
        case dragAreaScreenFrame, dragEvents, lastMouseEvent, pressed, dropCompleted
        case frontmost, keyWindow, minimized, visible
    }

    func encode(to encoder: Encoder) throws {
        var values = encoder.container(keyedBy: CodingKeys.self)
        try values.encode(key, forKey: .key)
        try values.encode(windowNumber, forKey: .windowNumber)
        try values.encode(width, forKey: .width)
        try values.encode(height, forKey: .height)
        try values.encode(clickCount, forKey: .clickCount)
        try values.encode(frame, forKey: .frame)
        try values.encode(buttonScreenFrame, forKey: .buttonScreenFrame)
        try values.encode(dragAreaScreenFrame, forKey: .dragAreaScreenFrame)
        try values.encode(dragEvents, forKey: .dragEvents)
        // Keep the field present (null) even before the first input event.
        try values.encode(lastMouseEvent, forKey: .lastMouseEvent)
        try values.encode(pressed, forKey: .pressed)
        try values.encode(dropCompleted, forKey: .dropCompleted)
        try values.encode(frontmost, forKey: .frontmost)
        try values.encode(keyWindow, forKey: .keyWindow)
        try values.encode(minimized, forKey: .minimized)
        try values.encode(visible, forKey: .visible)
    }
}

private enum WindowMutation: String, Codable {
    case move, resize, minimize, restore, close
}

private struct ControlRequest: Decodable {
    let id: String
    let window: String
    let operation: WindowMutation
}

private struct ControlResult: Encodable {
    let id: String
    let window: String
    let operation: WindowMutation
    let success: Bool
}

private struct Snapshot: Encodable {
    let processID: Int32
    let coordinateSystem = "CG-global-top-left-points"
    // OS-wide button mask, independent of whether either view received an up.
    let pressedMouseButtons: Int = Int(NSEvent.pressedMouseButtons)
    let frontmost: Bool
    let windows: [WindowState]
    let controlResult: ControlResult?
}

@MainActor
private final class FixtureWindow {
    let key: String
    let window: NSWindow
    let button: NSButton
    let dragArea = DragAreaView(frame: .zero)
    let counter = NSTextField(labelWithString: "Clicks: 0")
    var clickCount = 0

    init(key: String, frame: NSRect, target: AnyObject, action: Selector) {
        self.key = key
        window = NSWindow(contentRect: frame,
                          styleMask: [.titled, .closable, .miniaturizable, .resizable],
                          backing: .buffered, defer: false)
        window.title = Fixture.title
        window.identifier = NSUserInterfaceItemIdentifier("fixture-window-\(key)")
        window.isReleasedWhenClosed = false
        window.isRestorable = false
        window.tabbingMode = .disallowed
        window.contentMinSize = Fixture.minimumSize

        let heading = NSTextField(labelWithString: "Window \(key)")
        heading.font = .systemFont(ofSize: 52, weight: .bold)
        heading.alignment = .center
        button = NSButton(title: "Click \(key)", target: target, action: action)
        button.identifier = NSUserInterfaceItemIdentifier("fixture-button-\(key)")
        button.bezelStyle = .regularSquare
        button.font = .systemFont(ofSize: 30, weight: .semibold)
        counter.font = .monospacedDigitSystemFont(ofSize: 26, weight: .medium)
        counter.alignment = .center

        dragArea.identifier = NSUserInterfaceItemIdentifier("fixture-drag-area-\(key)")
        let stack = NSStackView(views: [heading, button, counter, dragArea])
        stack.orientation = .vertical
        stack.alignment = .centerX
        stack.spacing = 24
        stack.translatesAutoresizingMaskIntoConstraints = false
        let content = window.contentView!
        content.addSubview(stack)
        NSLayoutConstraint.activate([
            stack.centerXAnchor.constraint(equalTo: content.centerXAnchor),
            stack.centerYAnchor.constraint(equalTo: content.centerYAnchor),
            button.widthAnchor.constraint(equalToConstant: 260),
            button.heightAnchor.constraint(equalToConstant: 80),
            dragArea.widthAnchor.constraint(equalToConstant: Fixture.dragAreaSize.width),
            dragArea.heightAnchor.constraint(equalToConstant: Fixture.dragAreaSize.height),
        ])
    }

    func snapshot(primaryTop: CGFloat, frontWindow: NSWindow?) -> WindowState {
        window.contentView?.layoutSubtreeIfNeeded()
        let buttonFrame = window.convertToScreen(button.convert(button.bounds, to: nil))
        let dragFrame = window.convertToScreen(dragArea.convert(dragArea.bounds, to: nil))
        return WindowState(key: key, windowNumber: window.windowNumber,
                           width: window.frame.width, height: window.frame.height,
                           clickCount: clickCount,
                           frame: ScreenFrame(window.frame, primaryTop: primaryTop),
                           buttonScreenFrame: ScreenFrame(buttonFrame, primaryTop: primaryTop),
                           dragAreaScreenFrame: ScreenFrame(dragFrame, primaryTop: primaryTop),
                           dragEvents: dragArea.dragEvents, lastMouseEvent: dragArea.lastMouseEvent,
                           pressed: dragArea.pressed, dropCompleted: dragArea.dropCompleted,
                           frontmost: NSApp.isActive && window === frontWindow,
                           keyWindow: window.isKeyWindow, minimized: window.isMiniaturized, visible: window.isVisible)
    }
}

@MainActor
private final class FixtureDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    private let output: URL
    private let control: URL?
    private var controlResult: ControlResult?
    private var seenRequests: Set<String> = []
    private let encoder = JSONEncoder()
    private var windows: [FixtureWindow] = []
    private var timer: Timer?
    private var ready = false

    init(output: URL, control: URL?) {
        self.output = output
        self.control = control
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSWindow.allowsAutomaticWindowTabbing = false
        guard let screen = NSScreen.screens.first else {
            fail("No display available")
        }
        let visible = screen.visibleFrame
        // Distinct sizes and a horizontal offset keep both buttons exposed on
        // ordinary displays while permitting a little overlap on small screens.
        let sizeA = NSSize(width: 480, height: 500)
        let sizeB = NSSize(width: 440, height: 470)
        let originA = NSPoint(x: visible.minX + 24, y: visible.maxY - sizeA.height - 60)
        let originB = NSPoint(x: max(originA.x + 180, visible.maxX - sizeB.width - 24),
                              y: visible.maxY - sizeB.height - 130)
        let frames = [NSRect(origin: originA, size: sizeA), NSRect(origin: originB, size: sizeB)]
        for (key, frame) in zip(Fixture.keys, frames) {
            let item = FixtureWindow(key: key, frame: frame, target: self, action: #selector(click(_:)))
            item.dragArea.onEvent = { [weak self] in self?.publish() }
            // Account for title-bar height when keeping the enlarged fixture
            // inside the display's usable area.
            item.window.setFrame(item.window.constrainFrameRect(item.window.frame, to: screen), display: false)
            item.window.delegate = self
            windows.append(item)
            item.window.makeKeyAndOrderFront(nil)
        }
        ready = true
        NSApp.activate(ignoringOtherApps: true)
        publish()
        let refresh = Timer(timeInterval: Fixture.refreshInterval, target: self,
                            selector: #selector(refreshState), userInfo: nil, repeats: true)
        timer = refresh
        RunLoop.main.add(refresh, forMode: .common)
    }

    @objc private func click(_ sender: NSButton) {
        guard let item = windows.first(where: { $0.button === sender }) else { return }
        item.clickCount += 1
        item.counter.stringValue = "Clicks: \(item.clickCount)"
        publish()
    }

    // Test-only fault injection into this fixture's own NSWindows. It never
    // activates or changes another application's windows or emits input.
    private func mutateWindow() {
        guard let control, let data = try? Data(contentsOf: control), data.count <= 4096,
              let request = try? JSONDecoder().decode(ControlRequest.self, from: data),
              !request.id.isEmpty, request.id.count <= 128,
              seenRequests.count < 256, seenRequests.insert(request.id).inserted else { return }
        let item = windows.first { $0.key == request.window }
        controlResult = ControlResult(id: request.id, window: request.window,
                                      operation: request.operation, success: item != nil)
        guard let item else { return }
        switch request.operation {
        case .move:
            var frame = item.window.frame
            frame.origin.x += Fixture.geometryStep
            frame.origin.y -= Fixture.geometryStep
            item.window.setFrame(frame, display: true)
        case .resize:
            var size = item.window.contentView!.bounds.size
            size.width = max(Fixture.minimumSize.width, size.width - Fixture.geometryStep)
            size.height = max(Fixture.minimumSize.height, size.height - Fixture.geometryStep)
            item.window.setContentSize(size)
        case .minimize: item.window.miniaturize(nil)
        case .restore:
            item.window.deminiaturize(nil)
            item.window.makeKeyAndOrderFront(nil)
        case .close: item.window.close()
        }
    }

    @objc private func refreshState() { mutateWindow(); publish() }
    func windowDidBecomeKey(_ notification: Notification) { publish() }
    func windowDidResignKey(_ notification: Notification) { publish() }
    func applicationDidBecomeActive(_ notification: Notification) { publish() }
    func applicationDidResignActive(_ notification: Notification) { publish() }

    func windowWillClose(_ notification: Notification) {
        guard let closing = notification.object as? NSWindow else { return }
        windows.removeAll { $0.window === closing }
        publish()
        if windows.isEmpty { NSApp.terminate(nil) }
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { true }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply { .terminateNow }
    func applicationWillTerminate(_ notification: Notification) { timer?.invalidate() }

    private func publish() {
        guard ready, let primaryScreen = NSScreen.screens.first else { return }
        // NSScreen.main follows the key window and is NOT the global origin.
        // AppKit's first screen defines the primary bottom-left origin. Flip
        // about its top edge, never about the current window's display height.
        let primaryTop = primaryScreen.frame.maxY
        let frontWindow = NSApp.orderedWindows.first { $0.isVisible && !$0.isMiniaturized }
        let state = Snapshot(processID: ProcessInfo.processInfo.processIdentifier,
                             frontmost: NSApp.isActive,
                             windows: windows.map { $0.snapshot(primaryTop: primaryTop, frontWindow: frontWindow) },
                             controlResult: controlResult)
        do {
            try encoder.encode(state).write(to: output, options: .atomic)
        } catch {
            fail("Cannot write state: \(error)")
        }
    }
}

private func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data("window-fixture: \(message)\n".utf8))
    exit(EXIT_FAILURE)
}

@main
private struct WindowFixtureMain {
    @MainActor static func main() {
        let args = CommandLine.arguments
        guard (args.count == 3 || args.count == 5), args[1] == "--output", (args[2] as NSString).isAbsolutePath,
              args.count == 3 || (args[3] == "--control" && (args[4] as NSString).isAbsolutePath && args[4] != args[2]) else {
            fail("Usage: window-fixture --output <absolute-json-path> [--control <absolute-json-path>]")
        }
        let output = URL(fileURLWithPath: args[2])
        do {
            try FileManager.default.createDirectory(at: output.deletingLastPathComponent(),
                                                    withIntermediateDirectories: true,
                                                    attributes: [.posixPermissions: 0o700])
        } catch {
            fail("Cannot prepare output directory: \(error)")
        }
        let application = NSApplication.shared
        application.setActivationPolicy(.regular)
        let delegate = FixtureDelegate(output: output, control: args.count == 5 ? URL(fileURLWithPath: args[4]) : nil)
        application.delegate = delegate
        withExtendedLifetime(delegate) { application.run() }
    }
}
