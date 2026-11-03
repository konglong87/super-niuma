import Foundation
import CoreGraphics

// A generic launch-time blank-content guard, not proof that an app's UI is
// usable. Ignore chrome/shadows, normalize pixel layout and alpha, and require
// variation inside the content body. The model still verifies the real image.
enum WindowContentReadiness {
    private static let sampleSize = 32
    private static let minimumDiverseSamples = 8
    static func hasRenderedContent(_ image: CGImage) -> Bool {
        let body = CGRect(x: Double(image.width) * 0.10, y: Double(image.height) * 0.15,
                          width: Double(image.width) * 0.80, height: Double(image.height) * 0.75).integral
        guard let cropped = image.cropping(to: body), let colorSpace = CGColorSpace(name: CGColorSpace.sRGB) else { return false }
        var pixels = [UInt8](repeating: 0, count: sampleSize * sampleSize * 4)
        let drawn = pixels.withUnsafeMutableBytes { bytes -> Bool in
            guard let context = CGContext(data: bytes.baseAddress, width: sampleSize, height: sampleSize,
                bitsPerComponent: 8, bytesPerRow: sampleSize * 4, space: colorSpace,
                bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue | CGBitmapInfo.byteOrder32Big.rawValue) else { return false }
            let rect = CGRect(x: 0, y: 0, width: sampleSize, height: sampleSize)
            context.setFillColor(CGColor(gray: 1, alpha: 1)); context.fill(rect)
            context.draw(cropped, in: rect)
            return true
        }
        guard drawn else { return false }
        var histogram: [Int: Int] = [:]
        for offset in stride(from: 0, to: pixels.count, by: 4) {
            let color = (Int(pixels[offset]) >> 4) << 8 | (Int(pixels[offset + 1]) >> 4) << 4 | Int(pixels[offset + 2]) >> 4
            histogram[color, default: 0] += 1
        }
        let dominant = histogram.values.max() ?? 0
        return sampleSize * sampleSize - dominant >= minimumDiverseSamples
    }
}


enum LaunchBindingFailureReason {
    static func classify(readyCandidateCount: Int, selectedCandidate: Bool,
                         activationAttempted: Bool, activationSucceeded: Bool,
                         confirmedFrontmost: Bool) -> String {
        guard readyCandidateCount > 0 else { return "no_ready_window" }
        guard selectedCandidate else { return "ambiguous_ready_windows" }
        if activationAttempted && !activationSucceeded { return "window_activation_failed" }
        guard confirmedFrontmost else { return "window_not_frontmost" }
        return ""
    }
}

// Do not bind a transient splash/full-screen animation window. Ambiguity is
// observed within the existing bounded launch budget, never randomly resolved.
struct LaunchWindowStability {
    private static let requiredStableSeconds: TimeInterval = 0.10
    // Window Server exposes menu-bar and title-bar companion windows under the
    // same bundle. They can contain enough pixel variation to look rendered,
    // but they are never valid Computer Use targets. Prefer a substantial
    // content window when one exists; retain the old candidate behavior when
    // no substantial window is present so this is not an app-size bypass.
    private static let substantialWidth: CGFloat = 240
    private static let substantialHeight: CGFloat = 160
    private var previous: NativeWindow?
    private var stableSince: TimeInterval = 0
    mutating func update(_ candidate: NativeWindow?, contentReady: Bool, uptime: TimeInterval) -> NativeWindow? {
        guard let candidate, contentReady else { previous = nil; return nil }
        if let previous, previous.matchesIdentity(candidate), previous.frame == candidate.frame,
           previous.displayID == candidate.displayID {
            if uptime - stableSince >= Self.requiredStableSeconds { return candidate }
        } else {
            stableSince = uptime
        }
        previous = candidate
        return nil
    }
    static func substantialCandidates(from windows: [NativeWindow]) -> [NativeWindow] {
        windows.filter {
            $0.frame.width >= Self.substantialWidth && $0.frame.height >= Self.substantialHeight
        }
    }
    static func candidate(from windows: [NativeWindow]) -> NativeWindow? {
        let substantial = substantialCandidates(from: windows)
        let candidates = substantial.isEmpty ? windows : substantial
        let frontmost = candidates.filter(\.isFrontmost)
        if frontmost.count == 1 { return frontmost[0] }
        return candidates.count == 1 ? candidates[0] : nil
    }
}
