package io.github.konglong87.superniuma.mobile

import android.accessibilityservice.AccessibilityService
import android.graphics.Rect
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import io.github.konglong87.superniuma.niumacore.Niumacore
import java.util.Locale

/**
 * M0's accessibility service. It only reads: when measuring is on and another
 * app's window comes to the front, it reads that window's elements once the
 * screen has been quiet for [SETTLE_MS], hands the snapshot to the Go core and
 * logs the timings under the tag [MainActivity.TAG]. It never acts on a screen.
 */
class UiTreeService : AccessibilityService() {
    private lateinit var worker: HandlerThread
    private lateinit var handler: Handler
    @Volatile
    private var pendingPackage: String? = null

    override fun onServiceConnected() {
        worker = HandlerThread("ui-tree").also { it.start() }
        handler = Handler(worker.looper)
        instance = this
        Log.i(MainActivity.TAG, "service connected")
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent) {
        if (!measuring || event.eventType != AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) return
        val pkg = event.packageName?.toString() ?: return
        if (pkg == packageName) return
        pendingPackage = pkg
        // Debounce window reads only; an on-request read stays queued.
        handler.removeCallbacksAndMessages(SETTLE_TOKEN)
        handler.postDelayed({ measure("window $pkg", pkg) }, SETTLE_TOKEN, SETTLE_MS)
    }

    override fun onInterrupt() {}

    override fun onDestroy() {
        instance = null
        if (::worker.isInitialized) worker.quitSafely()
        super.onDestroy()
    }

    /** Reads the active window now, on the service's worker thread. */
    fun measureNow(reason: String) {
        handler.post { measure(reason, null) }
    }

    /**
     * Reads one window. [expected] is the package whose window event asked for
     * the read; the fallback never reads another app's window in its place.
     */
    private fun measure(reason: String, expected: String?, retried: Boolean = false) {
        val start = SystemClock.elapsedRealtimeNanos()
        val root = activeRoot(expected)
        if (root == null) {
            // A window in transition (e.g. a deep-link trampoline) has no root yet.
            if (!retried) {
                handler.postDelayed({ measure(reason, expected, retried = true) }, SETTLE_TOKEN, RETRY_MS)
            } else {
                Log.w(MainActivity.TAG, "tree {\"ok\":false,\"reason\":\"$reason\",\"error\":\"no readable window\"}")
            }
            return
        }
        val pkg = root.packageName?.toString() ?: pendingPackage ?: "unknown"
        val nodes = readTree(root)
        val read = SystemClock.elapsedRealtimeNanos()
        val json = UiSnapshot.encode(pkg, nodes)
        val encoded = SystemClock.elapsedRealtimeNanos()
        val summary = Niumacore.screenSummary(json)
        val handed = SystemClock.elapsedRealtimeNanos()
        Log.i(
            MainActivity.TAG,
            "tree {\"ok\":true,\"reason\":\"$reason\",\"package\":\"$pkg\",\"nodes\":${nodes.size}," +
                "\"read_ms\":${ms(start, read)},\"encode_ms\":${ms(read, encoded)}," +
                "\"go_ms\":${ms(encoded, handed)},\"total_ms\":${ms(start, handed)}," +
                "\"json_bytes\":${json.length},\"core\":$summary}",
        )
        UiSnapshot.describe(nodes, 12).forEach { Log.i(MainActivity.TAG, "tree-line $it") }
    }

    /**
     * The active window's root, or else the root of the active app window:
     * some screens, such as the Wi-Fi page reached through a deep link, report
     * no active-window root. Either must belong to [expected] when it is set.
     */
    private fun activeRoot(expected: String?): AccessibilityNodeInfo? {
        val root = rootInActiveWindow
            ?: windows.firstOrNull { it.type == AccessibilityWindowInfo.TYPE_APPLICATION && it.isActive }?.root
            ?: return null
        return root.takeIf { expected == null || root.packageName?.toString() == expected }
    }

    private fun readTree(root: AccessibilityNodeInfo): List<UiNode> {
        val nodes = ArrayList<UiNode>(256)
        val stack = ArrayDeque<Pair<AccessibilityNodeInfo, Int>>()
        stack.addLast(root to -1)
        val bounds = Rect()
        while (stack.isNotEmpty() && nodes.size < MAX_NODES) {
            val (info, parent) = stack.removeLast()
            info.getBoundsInScreen(bounds)
            val index = nodes.size
            nodes += UiNode(
                parent = parent,
                cls = info.className?.toString() ?: "",
                text = info.text?.toString(),
                desc = info.contentDescription?.toString(),
                viewId = info.viewIdResourceName,
                left = bounds.left,
                top = bounds.top,
                right = bounds.right,
                bottom = bounds.bottom,
                clickable = info.isClickable,
                editable = info.isEditable,
                scrollable = info.isScrollable,
            )
            for (i in info.childCount - 1 downTo 0) {
                info.getChild(i)?.let { stack.addLast(it to index) }
            }
        }
        return nodes
    }

    private fun ms(from: Long, to: Long) = String.format(Locale.ROOT, "%.1f", (to - from) / 1_000_000.0)

    companion object {
        private const val SETTLE_MS = 800L
        private const val RETRY_MS = 500L
        private val SETTLE_TOKEN = Any()
        private const val MAX_NODES = 2000

        @Volatile
        var measuring = false

        @Volatile
        var instance: UiTreeService? = null
    }
}
