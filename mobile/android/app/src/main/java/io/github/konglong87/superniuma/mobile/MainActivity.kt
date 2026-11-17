package io.github.konglong87.superniuma.mobile

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.provider.Settings
import android.text.InputType
import android.util.Log
import android.view.View
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import io.github.konglong87.superniuma.niumacore.Niumacore
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

/**
 * The person's screen: model settings, a task with the apps it may use and
 * the text that proves it is done, Start/Stop, the pending approval, and the
 * M0 probes.
 *
 * Debug builds also accept test commands (`m0` extra) through the
 * `.M0Commands` alias, which only `adb shell` may start. Release builds
 * disable the alias and ignore extras.
 */
class MainActivity : Activity() {
    private val worker = Executors.newSingleThreadExecutor()
    private lateinit var secrets: SecretStore
    private lateinit var readerStatus: TextView
    private lateinit var openSettings: Button
    private lateinit var baseUrl: EditText
    private lateinit var model: EditText
    private lateinit var key: EditText
    private lateinit var goal: EditText
    private lateinit var apps: EditText
    private lateinit var expect: EditText
    private lateinit var taskStatus: TextView
    private lateinit var approvalPanel: LinearLayout
    private lateinit var approvalText: TextView
    private lateinit var output: TextView
    private val render: (TaskSnapshot) -> Unit = { show(it) }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        secrets = SecretStore(this)
        setContentView(ScrollView(this).apply { addView(buildUi()) })
        StateHub.observe(render)
        // Approvals and results reach the person through notifications too.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
            checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED
        ) {
            requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 1)
        }
        // A recreated activity (rotation, theme change) keeps its intent; run
        // the command only on the first creation, never again.
        if (savedInstanceState == null) handle(intent)
    }

    override fun onResume() {
        super.onResume()
        refreshReader()
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    override fun onDestroy() {
        StateHub.forget(render)
        worker.shutdown()
        super.onDestroy()
    }

    private fun buildUi(): View = LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(40, 32, 40, 32)
        readerStatus = TextView(context).apply { textSize = 14f }
        openSettings = button("打开无障碍设置") { startActivity(Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS)) }
        addView(readerStatus)
        addView(openSettings)

        addView(header("模型"))
        val saved = secrets.load()
        baseUrl = field("服务地址，如 https://…/v1", saved?.baseUrl)
        model = field("模型名称", saved?.model)
        key = field(if (secrets.hasRoute()) "密钥已保存（留空则不修改）" else "API 密钥", null).apply {
            inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
        }
        addView(baseUrl)
        addView(model)
        addView(key)
        addView(button("保存模型设置") { saveRoute() })

        addView(header("任务"))
        goal = field("要做什么，例如：设一个早上 7:00 的闹钟", null).apply { minLines = 2 }
        apps = field("允许使用的应用包名，逗号分隔", "com.google.android.deskclock")
        expect = field("完成后屏幕上应出现的文字（可选，逗号分隔）", null)
        addView(goal)
        addView(apps)
        addView(expect)
        addView(LinearLayout(context).apply {
            orientation = LinearLayout.HORIZONTAL
            addView(button("开始") { startFromForm() }, LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
            addView(button("停止") { stopTask() }, LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
        })
        approvalText = TextView(context).apply { textSize = 15f }
        approvalPanel = LinearLayout(context).apply {
            orientation = LinearLayout.VERTICAL
            visibility = View.GONE
            addView(header("需要你确认"))
            addView(approvalText)
            addView(LinearLayout(context).apply {
                orientation = LinearLayout.HORIZONTAL
                addView(button("拒绝") { decide("deny", null) }, LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
                addView(button("允许一次") { decide("allow_once", null) }, LinearLayout.LayoutParams(0, WRAP_CONTENT, 1f))
            })
        }
        addView(approvalPanel)
        taskStatus = TextView(context).apply { textSize = 13f; setTextIsSelectable(true) }
        addView(taskStatus)

        addView(header("诊断"))
        addView(button("Core probe") { run("probe", null) })
        addView(button("HTTPS probe") { run("https", null) })
        addView(button("Verify probe") { run("verify", null) })
        output = TextView(context).apply { setTextIsSelectable(true); textSize = 12f }
        addView(output, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT))
    }

    private fun refreshReader() {
        val on = UiTreeService.instance != null
        val notices = if (Notifier.allowed(this)) "通知已开启" else "通知未开启：审批只能在悬浮卡片和这里处理"
        readerStatus.text = "核心 ${Niumacore.version()} · 屏幕助手${if (on) "已开启" else "未开启：任务需要在系统无障碍设置里打开“超级牛马 · 屏幕助手”"} · $notices"
        openSettings.visibility = if (on) View.GONE else View.VISIBLE
    }

    private fun show(s: TaskSnapshot) {
        refreshReader()
        val p = s.pending
        approvalPanel.visibility = if (s.running && p != null) View.VISIBLE else View.GONE
        if (p != null) approvalText.text = "牛马想要：${p.what}\n应用：${p.app}\n理由：${p.intent.ifBlank { "（未说明）" }}"
        if (s.id == null) {
            taskStatus.text = "还没有任务"
            return
        }
        val lines = mutableListOf("任务：${s.goal}", "状态：${statusName(s)}${if (s.code.isNotBlank()) "（${s.code}）" else ""}")
        s.retry?.let { lines += "${it.reason}，${(it.waitMs + 999) / 1000} 秒后重试（第 ${it.retry}/${it.max} 次）" }
        if (s.result.isNotBlank()) lines += "牛马的说明：${s.result}"
        s.steps.takeLast(12).forEach { st ->
            lines += "  ${st.seq}. ${st.kind} ${st.target.take(30)} → ${st.outcome}${if (st.decision.isNotBlank()) "（${st.decision}）" else ""}"
        }
        taskStatus.text = lines.joinToString("\n")
    }

    private fun statusName(s: TaskSnapshot) = when (s.status) {
        "running" -> "进行中"
        "done" -> if (s.verified == true) "完成，已验收" else "完成（未设置验收文字）"
        "needs_attention" -> "需要你检查：验收没有通过"
        "failed" -> "失败"
        "stopped" -> "已停止"
        "interrupted" -> "中断（应用曾被关闭，不会自动重试）"
        else -> s.status ?: ""
    }

    private fun saveRoute() {
        val existing = secrets.load()
        val k = key.text.toString().ifBlank { existing?.key ?: "" }
        if (baseUrl.text.isBlank() || model.text.isBlank() || k.isBlank()) {
            log("route", "{\"ok\":false,\"error\":\"服务地址、模型和密钥都需要填写\"}")
            return
        }
        secrets.save(ModelRoute(baseUrl.text.toString().trim(), model.text.toString().trim(), k))
        key.setText("")
        key.hint = "密钥已保存（留空则不修改）"
        log("route", "{\"ok\":true}")
    }

    private fun startFromForm() = startTask(goal.text.toString(), split(apps.text.toString()), split(expect.text.toString()), 0)

    private fun startTask(goal: String, apps: List<String>, expect: List<String>, minutes: Int) {
        val route = secrets.load()
        if (route == null) {
            log("start", "{\"ok\":false,\"error\":\"先保存模型设置\"}")
            return
        }
        val spec = JSONObject()
            .put("goal", goal)
            .put("apps", JSONArray(apps))
            .put("expect", JSONArray(expect))
            .put("route", JSONObject().put("base_url", route.baseUrl).put("model", route.model).put("token", route.key))
        if (minutes > 0) spec.put("max_minutes", minutes)
        worker.execute { log("start", Niumacore.startTask(spec.toString())) }
    }

    private fun stopTask() = worker.execute { log("stop", Niumacore.stopTask()) }

    private fun decide(decision: String, id: String?) {
        val target = id ?: StateHub.latest?.pending?.id
        if (target == null) {
            log("decide", "{\"ok\":false,\"error\":\"no pending approval\"}")
            return
        }
        val result = Niumacore.decide(target, decision)
        log("decide", result)
        // This screen covers the app the step runs in; step aside so it is in
        // front again (the core waits a few seconds for that).
        if (decision == "allow_once" && JSONObject(result).optBoolean("ok")) moveTaskToBack(true)
    }

    private fun split(s: String) = s.split(',', '，').map { it.trim() }.filter { it.isNotEmpty() }

    // --- test commands (debug builds, adb shell only) ---

    private fun handle(intent: Intent?) {
        // Only the DUMP-protected alias carries test commands; the launcher
        // entry ignores extras, so other apps cannot trigger these actions.
        if (!BuildConfig.DEBUG || intent?.component?.className != COMMANDS_ALIAS) return
        val action = intent.getStringExtra(EXTRA_ACTION) ?: return
        run(action, intent)
    }

    private fun run(action: String, intent: Intent?) {
        when (action) {
            "measure_on", "measure_off" -> {
                UiTreeService.measuring = action == "measure_on"
                log(action, "{\"ok\":${UiTreeService.instance != null},\"measuring\":${UiTreeService.measuring}}")
                return
            }
            "tree" -> {
                val service = UiTreeService.instance
                if (service == null) {
                    log(action, "{\"ok\":false,\"error\":\"screen reader is off\"}")
                } else {
                    service.measureNow("on request")
                    // Queued only; the read logs its own `tree {"ok":...}` line.
                    log(action, "{\"queued\":true}")
                }
                return
            }
            "m1_route" -> {
                // The token is a test credential for a loopback relay; it goes
                // through the same Keystore path as a real key.
                val r = ModelRoute(intent?.getStringExtra("base_url") ?: DEFAULT_RELAY_URL, intent?.getStringExtra("model") ?: "", intent?.getStringExtra("token") ?: "")
                if (r.model.isBlank() || r.key.isBlank()) {
                    log(action, "{\"ok\":false,\"error\":\"model and token extras are required\"}")
                    return
                }
                secrets.save(r)
                log(action, "{\"ok\":${secrets.load() == r}}")
                return
            }
            "m1_start" -> {
                startTask(
                    intent?.getStringExtra("goal") ?: "",
                    split(intent?.getStringExtra("apps") ?: ""),
                    (intent?.getStringExtra("expect") ?: "").split('|').map { it.trim() }.filter { it.isNotEmpty() },
                    intent?.getStringExtra("max_minutes")?.toIntOrNull() ?: 0,
                )
                return
            }
            "m1_stop" -> { stopTask(); return }
            "m1_decide" -> { decide(intent?.getStringExtra("decision") ?: "", intent?.getStringExtra("id")); return }
            "m1_state" -> { log(action, Niumacore.taskState()); return }
        }
        worker.execute {
            val workspace = File(filesDir, "m0-workspace").apply { mkdirs() }.canonicalPath
            val result = when (action) {
                "probe" -> Niumacore.probe(filesDir.canonicalPath)
                "https" -> Niumacore.httpsProbe(intent?.getStringExtra("url") ?: DEFAULT_HTTPS_URL)
                "verify" -> Niumacore.verifyProbe(workspace)
                "model" -> modelCall(intent, workspace)
                else -> "{\"ok\":false,\"error\":\"unknown action\"}"
            }
            log(action, result)
        }
    }

    private fun modelCall(intent: Intent?, workspace: String): String {
        val baseUrl = intent?.getStringExtra("base_url") ?: DEFAULT_RELAY_URL
        val model = intent?.getStringExtra("model") ?: return "{\"ok\":false,\"error\":\"model extra missing\"}"
        val token = intent.getStringExtra("token") ?: return "{\"ok\":false,\"error\":\"token extra missing\"}"
        val objective = intent.getStringExtra("objective") ?: "Reply with one short sentence confirming you received this from a phone."
        val route = "{\"base_url\":${UiSnapshot.jsonString(baseUrl)},\"model\":${UiSnapshot.jsonString(model)}," +
            "\"token\":${UiSnapshot.jsonString(token)}}"
        return Niumacore.modelCall(route, workspace, objective)
    }

    private fun log(action: String, json: String) {
        Log.i(TAG, "$action $json")
        runOnUiThread { output.append("$action → $json\n\n") }
    }

    private fun header(text: String) = TextView(this).apply { this.text = text; textSize = 17f; setPadding(0, 32, 0, 8) }

    private fun field(hint: String, value: String?) = EditText(this).apply { this.hint = hint; value?.let { setText(it) } }

    private fun button(label: String, onClick: () -> Unit) = Button(this).apply {
        text = label
        setOnClickListener { onClick() }
    }

    companion object {
        const val TAG = "NiumaM0"
        private const val EXTRA_ACTION = "m0"

        // The alias's class name comes from the manifest namespace, which is
        // this class's package; applicationId (packageName) may differ.
        private val COMMANDS_ALIAS = MainActivity::class.java.name.substringBeforeLast('.') + ".M0Commands"
        private const val DEFAULT_RELAY_URL = "http://127.0.0.1:18080/v1"
        private const val DEFAULT_HTTPS_URL = "https://connectivitycheck.gstatic.com/generate_204"
    }
}
