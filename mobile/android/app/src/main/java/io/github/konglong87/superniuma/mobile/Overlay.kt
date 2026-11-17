package io.github.konglong87.superniuma.mobile

import android.accessibilityservice.AccessibilityService
import android.graphics.Color
import android.graphics.PixelFormat
import android.graphics.drawable.GradientDrawable
import android.os.Handler
import android.os.Looper
import android.view.Gravity
import android.view.View
import android.view.WindowManager
import android.widget.Button
import android.widget.LinearLayout
import android.widget.TextView
import android.util.Log
import org.json.JSONArray
import org.json.JSONObject
import io.github.konglong87.superniuma.niumacore.Niumacore
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import kotlin.concurrent.thread

/**
 * Accessibility overlay windows over whatever app the agent drives: a status
 * bar with Stop while a task runs, and an approval card while a step waits.
 * They are never the active window, so the agent's snapshots do not include
 * them and its element actions cannot reach their buttons. All methods run
 * on the main thread unless noted.
 */
class Overlay(private val service: AccessibilityService) {
    private val wm = service.getSystemService(WindowManager::class.java)
    private val main = Handler(Looper.getMainLooper())
    private var bar: LinearLayout? = null
    private var barText: TextView? = null
    private var card: LinearLayout? = null
    private var cardId: String? = null
    private var passThrough = false

    fun render(s: TaskSnapshot) {
        if (s.running) showBar(s) else removeBar()
        val p = s.pending
        if (s.running && p != null) showCard(p) else removeCard()
    }

    fun hide() {
        removeBar()
        removeCard()
    }

    /**
     * Lets injected gestures reach the app under the bar (called from a
     * worker thread around a gesture tap or swipe).
     */
    fun setPassThrough(on: Boolean) {
        val done = CountDownLatch(1)
        main.post {
            passThrough = on
            bar?.let { wm.updateViewLayout(it, barParams()) }
            done.countDown()
        }
        done.await(500, TimeUnit.MILLISECONDS)
    }

    private fun showBar(s: TaskSnapshot) {
        val last = s.steps.lastOrNull()
        val text = "超级牛马 · 第 ${s.steps.size} 步" + (last?.let { " · ${it.kind} ${it.target.take(24)}" } ?: "")
        if (bar == null) {
            barText = TextView(service).apply { setTextColor(Color.WHITE); textSize = 13f }
            val stop = Button(service).apply {
                this.text = "停止"
                setOnClickListener { thread(name = "stop-task") { Niumacore.stopTask() } }
            }
            bar = LinearLayout(service).apply {
                orientation = LinearLayout.HORIZONTAL
                gravity = Gravity.CENTER_VERTICAL
                setPadding(32, 8, 16, 8)
                background = rounded(Color.argb(220, 30, 30, 30))
                addView(barText, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
                addView(stop)
            }
            wm.addView(bar, barParams())
            reportPositions("stop" to stop)
        }
        barText?.text = text
    }

    private fun showCard(p: PendingApproval) {
        if (card != null && cardId == p.id) return
        removeCard()
        val title = TextView(service).apply { text = "需要你确认"; textSize = 18f; setTextColor(Color.BLACK) }
        val body = TextView(service).apply {
            text = "牛马想要：${p.what}\n应用：${p.app}\n理由：${p.intent.ifBlank { "（未说明）" }}\n60 秒内不处理将自动拒绝。"
            textSize = 14f
            setTextColor(Color.DKGRAY)
            setPadding(0, 16, 0, 16)
        }
        val allow = Button(service).apply { text = "允许一次"; setOnClickListener { Niumacore.decide(p.id, "allow_once") } }
        val deny = Button(service).apply { text = "拒绝"; setOnClickListener { Niumacore.decide(p.id, "deny") } }
        val buttons = LinearLayout(service).apply {
            orientation = LinearLayout.HORIZONTAL
            addView(deny, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
            addView(allow, LinearLayout.LayoutParams(0, LinearLayout.LayoutParams.WRAP_CONTENT, 1f))
        }
        card = LinearLayout(service).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(48, 40, 48, 32)
            background = rounded(Color.WHITE)
            elevation = 24f
            addView(title)
            addView(body)
            addView(buttons)
        }
        cardId = p.id
        wm.addView(card, params(Gravity.BOTTOM, WindowManager.LayoutParams.MATCH_PARENT, touchable = true))
        reportPositions("allow" to allow, "deny" to deny)
    }

    /**
     * Debug builds log where the buttons are on screen, so a test script can
     * press them with a real touch, as a person would.
     */
    private fun reportPositions(vararg buttons: Pair<String, View>) {
        if (!BuildConfig.DEBUG) return
        buttons.first().second.post {
            val o = JSONObject()
            for ((name, view) in buttons) {
                val at = IntArray(2).also { view.getLocationOnScreen(it) }
                o.put(name, JSONArray(listOf(at[0] + view.width / 2, at[1] + view.height / 2)))
            }
            Log.i(MainActivity.TAG, "overlay $o")
        }
    }

    private fun removeBar() {
        bar?.let { wm.removeView(it) }
        bar = null
        barText = null
    }

    private fun removeCard() {
        card?.let { wm.removeView(it) }
        card = null
        cardId = null
    }

    private fun barParams() = params(Gravity.TOP, WindowManager.LayoutParams.MATCH_PARENT, touchable = !passThrough)

    private fun params(gravity: Int, width: Int, touchable: Boolean) = WindowManager.LayoutParams(
        width,
        WindowManager.LayoutParams.WRAP_CONTENT,
        WindowManager.LayoutParams.TYPE_ACCESSIBILITY_OVERLAY,
        WindowManager.LayoutParams.FLAG_NOT_FOCUSABLE or WindowManager.LayoutParams.FLAG_LAYOUT_IN_SCREEN or
            (if (touchable) 0 else WindowManager.LayoutParams.FLAG_NOT_TOUCHABLE),
        PixelFormat.TRANSLUCENT,
    ).apply {
        this.gravity = gravity
        y = if (gravity == Gravity.TOP) 120 else 48
    }

    private fun rounded(color: Int) = GradientDrawable().apply {
        setColor(color)
        cornerRadius = 32f
    }
}
