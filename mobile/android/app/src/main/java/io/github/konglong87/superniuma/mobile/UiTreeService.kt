package io.github.konglong87.superniuma.mobile

import android.accessibilityservice.AccessibilityService
import android.accessibilityservice.GestureDescription
import android.content.Intent
import android.graphics.Path
import android.graphics.Rect
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.HandlerThread
import android.os.SystemClock
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import android.view.accessibility.AccessibilityNodeInfo
import android.view.accessibility.AccessibilityWindowInfo
import io.github.konglong87.superniuma.niumacore.DeviceBridge
import io.github.konglong87.superniuma.niumacore.Niumacore
import org.json.JSONObject
import java.util.Locale
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * The phone as the Go core sees it. As a [DeviceBridge] it reads the active
 * app window and performs one action at a time, then waits for the screen
 * to settle; the core decides what may run. It also keeps the M0 timing
 * reads (measure_on / tree) for comparison.
 */
class UiTreeService : AccessibilityService(), DeviceBridge {
    private lateinit var worker: HandlerThread
    private lateinit var handler: Handler
    private lateinit var overlay: Overlay
    private val renderOverlay: (TaskSnapshot) -> Unit = { overlay.render(it) }

    @Volatile
    private var pendingPackage: String? = null

    @Volatile
    private var lastEventAt = 0L

    private val bridgeLock = Any()
    private var latestNodes: List<AccessibilityNodeInfo> = emptyList()
    private var home: String? = null

    override fun onServiceConnected() {
        worker = HandlerThread("ui-tree").also { it.start() }
        handler = Handler(worker.looper)
        overlay = Overlay(this)
        home = packageManager.resolveActivity(Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_HOME), 0)
            ?.activityInfo?.packageName
        instance = this
        Niumacore.setBridge(this)
        StateHub.observe(renderOverlay)
        Log.i(MainActivity.TAG, "service connected")
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent) {
        lastEventAt = SystemClock.uptimeMillis()
        if (!measuring || event.eventType != AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED) return
        val pkg = event.packageName?.toString() ?: return
        if (pkg == packageName) return
        pendingPackage = pkg
        // Debounce window reads only; an on-request read stays queued.
        handler.removeCallbacksAndMessages(SETTLE_TOKEN)
        handler.postDelayed({ measure("window $pkg", pkg) }, SETTLE_TOKEN, SETTLE_MS)
    }

    override fun onInterrupt() {}

    override fun onUnbind(intent: Intent?): Boolean {
        disconnect()
        return super.onUnbind(intent)
    }

    override fun onDestroy() {
        disconnect()
        super.onDestroy()
    }

    /** Without the screen reader a task cannot continue safely: stop it. */
    private fun disconnect() {
        if (instance !== this) return
        instance = null
        Niumacore.clearBridge()
        StateHub.forget(renderOverlay)
        overlay.hide()
        if (StateHub.latest?.running == true) thread(name = "stop-task") { Niumacore.stopTask() }
        worker.quitSafely()
        Log.i(MainActivity.TAG, "service disconnected")
    }

    // --- DeviceBridge ---

    override fun snapshot(): String = synchronized(bridgeLock) {
        val root = activeRoot(null)
        val pkg = root?.packageName?.toString()
        if (root == null || pkg == null) {
            latestNodes = emptyList()
            return ""
        }
        val infos = ArrayList<AccessibilityNodeInfo>(256)
        val nodes = readTree(root, infos)
        latestNodes = infos
        UiSnapshot.encode(pkg, nodes, home)
    }

    override fun perform(actionJSON: String): String = synchronized(bridgeLock) {
        val result = try {
            val a = JSONObject(actionJSON)
            val target = latestNodes.getOrNull(a.optInt("index", -1))?.takeIf { it.refresh() }
            // Never act on an element whose app is no longer in front: a
            // click on a covered window can report success and do nothing.
            val front = activeRoot(null)?.packageName?.toString()
            if (target != null && target.packageName?.toString() != front) {
                debug("refused: ${target.packageName} is not in front ($front)")
                return failed("the app is no longer in front; read the screen again")
            }
            when (a.getString("kind")) {
                "tap" -> target?.let { tap(it) } ?: failed("the element is gone")
                "type" -> target?.let { type(it, a.optString("text"), a.optBoolean("submit")) } ?: failed("the element is gone")
                "swipe" -> swipe(target, a.optString("direction"), a.optInt("index", -1) >= 0)
                "key" -> key(a.optString("key"))
                "launch" -> launch(a.optString("app"))
                else -> failed("unknown action")
            }
        } catch (e: Exception) {
            failed("action could not run")
        }
        waitForSettle()
        result
    }

    private fun tap(node: AccessibilityNodeInfo): String {
        var target: AccessibilityNodeInfo? = node
        var depth = 0
        while (target != null && !target.isClickable && depth < 6) {
            target = target.parent
            depth++
        }
        val clicked = target != null && target.isClickable && target.performAction(AccessibilityNodeInfo.ACTION_CLICK)
        debug("tap ${describe(node)} via ${target?.let { describe(it) } ?: "none"} click=$clicked")
        if (clicked) return done()
        val r = Rect().also { node.getBoundsInScreen(it) }
        return gesture(Path().apply { moveTo(r.exactCenterX(), r.exactCenterY()) }, 60)
    }

    private fun type(node: AccessibilityNodeInfo, text: String, submit: Boolean): String {
        val args = Bundle().apply { putCharSequence(AccessibilityNodeInfo.ACTION_ARGUMENT_SET_TEXT_CHARSEQUENCE, text) }
        if (!node.performAction(AccessibilityNodeInfo.ACTION_SET_TEXT, args)) return failed("the field did not accept text")
        return if (submit) imeEnter(node) else done()
    }

    private fun swipe(node: AccessibilityNodeInfo?, direction: String, hasTarget: Boolean): String {
        val forward = direction == "down" || direction == "right"
        if (hasTarget) {
            node ?: return failed("the element is gone")
            val action = if (forward) AccessibilityNodeInfo.ACTION_SCROLL_FORWARD else AccessibilityNodeInfo.ACTION_SCROLL_BACKWARD
            return if (node.performAction(action)) done() else failed("cannot scroll further that way")
        }
        val m = resources.displayMetrics
        val (cx, cy) = m.widthPixels / 2f to m.heightPixels / 2f
        val dx = m.widthPixels * 0.3f
        val dy = m.heightPixels * 0.25f
        val path = Path().apply {
            when (direction) {
                "down" -> { moveTo(cx, cy + dy); lineTo(cx, cy - dy) }
                "up" -> { moveTo(cx, cy - dy); lineTo(cx, cy + dy) }
                "right" -> { moveTo(cx + dx, cy); lineTo(cx - dx, cy) }
                else -> { moveTo(cx - dx, cy); lineTo(cx + dx, cy) }
            }
        }
        return gesture(path, 350)
    }

    private fun key(key: String): String = when (key) {
        "back" -> if (performGlobalAction(GLOBAL_ACTION_BACK)) done() else failed("back not available")
        "home" -> if (performGlobalAction(GLOBAL_ACTION_HOME)) done() else failed("home not available")
        "enter" -> rootInActiveWindow?.findFocus(AccessibilityNodeInfo.FOCUS_INPUT)?.let { imeEnter(it) } ?: failed("no focused text field")
        else -> failed("unknown key")
    }

    private fun imeEnter(node: AccessibilityNodeInfo): String {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) return failed("enter needs Android 11")
        return if (node.performAction(AccessibilityNodeInfo.AccessibilityAction.ACTION_IME_ENTER.id)) done() else failed("the field has no action key")
    }

    private fun launch(app: String): String {
        val intent = packageManager.getLaunchIntentForPackage(app) ?: return failed("app not installed")
        startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED))
        return done()
    }

    /** Injects a gesture with the overlay letting touches through; uncertain if it was cancelled. */
    private fun gesture(path: Path, durationMs: Long): String {
        overlay.setPassThrough(true)
        try {
            val finished = CountDownLatch(1)
            var completed = false
            val stroke = GestureDescription.StrokeDescription(path, 0, durationMs)
            val sent = dispatchGesture(GestureDescription.Builder().addStroke(stroke).build(), object : GestureResultCallback() {
                override fun onCompleted(g: GestureDescription?) { completed = true; finished.countDown() }
                override fun onCancelled(g: GestureDescription?) { finished.countDown() }
            }, null)
            if (!sent) return failed("gesture not accepted")
            finished.await(3, TimeUnit.SECONDS)
            return if (completed) done() else uncertain()
        } finally {
            overlay.setPassThrough(false)
        }
    }

    /** Waits until no accessibility event arrived for [QUIET_MS], within [MIN_SETTLE_MS]..[MAX_SETTLE_MS]. */
    private fun waitForSettle() {
        val start = SystemClock.uptimeMillis()
        while (true) {
            val now = SystemClock.uptimeMillis()
            if (now - start >= MIN_SETTLE_MS && now - lastEventAt >= QUIET_MS) return
            if (now - start >= MAX_SETTLE_MS) return
            SystemClock.sleep(50)
        }
    }

    /** Debug builds log which element an action reached (never typed text). */
    private fun debug(line: String) {
        if (BuildConfig.DEBUG) Log.i(MainActivity.TAG, "perform $line")
    }

    /** Class, view id and bounds only: screen text can be private. */
    private fun describe(n: AccessibilityNodeInfo): String {
        val r = Rect().also { n.getBoundsInScreen(it) }
        return "${n.className?.toString()?.substringAfterLast('.')}/${n.viewIdResourceName?.substringAfterLast('/') ?: ""}$r"
    }

    private fun done() = "{\"status\":\"done\"}"
    private fun uncertain() = "{\"status\":\"uncertain\"}"
    private fun failed(why: String) = JSONObject().put("status", "failed").put("error", why).toString()

    // --- M0 timing reads ---

    /** Reads the active window now, on the service's worker thread. */
    fun measureNow(reason: String) {
        handler.post { measure(reason, null) }
    }

    private fun measure(reason: String, expected: String?, retried: Boolean = false) {
        val start = SystemClock.elapsedRealtimeNanos()
        val root = activeRoot(expected)
        if (root == null) {
            if (!retried) {
                handler.postDelayed({ measure(reason, expected, retried = true) }, SETTLE_TOKEN, RETRY_MS)
            } else {
                Log.w(MainActivity.TAG, "tree {\"ok\":false,\"reason\":\"$reason\",\"error\":\"no readable window\"}")
            }
            return
        }
        val pkg = root.packageName?.toString() ?: pendingPackage ?: "unknown"
        val nodes = readTree(root, null)
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

    private fun readTree(root: AccessibilityNodeInfo, infos: MutableList<AccessibilityNodeInfo>?): List<UiNode> {
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
                text = if (info.isPassword) null else info.text?.toString(),
                desc = info.contentDescription?.toString(),
                viewId = info.viewIdResourceName,
                left = bounds.left,
                top = bounds.top,
                right = bounds.right,
                bottom = bounds.bottom,
                clickable = info.isClickable,
                editable = info.isEditable,
                scrollable = info.isScrollable,
                password = info.isPassword,
                checked = info.isChecked,
            )
            infos?.add(info)
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
        private const val MAX_NODES = 2000
        private const val MIN_SETTLE_MS = 300L
        private const val QUIET_MS = 400L
        private const val MAX_SETTLE_MS = 3000L
        private val SETTLE_TOKEN = Any()

        @Volatile
        var measuring = false

        @Volatile
        var instance: UiTreeService? = null
    }
}
