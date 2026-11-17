package io.github.konglong87.superniuma.mobile

import android.app.Activity
import android.content.Intent
import android.os.Bundle
import android.util.Log
import android.view.ViewGroup.LayoutParams.MATCH_PARENT
import android.view.ViewGroup.LayoutParams.WRAP_CONTENT
import android.widget.Button
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import io.github.konglong87.superniuma.niumacore.Niumacore
import java.io.File
import java.util.concurrent.Executors

/**
 * M0 feasibility screen: runs the Go core's probes and shows each report.
 *
 * Debug builds also accept an `m0` extra through the `.M0Commands` alias, which
 * only `adb shell` may start (`am start -n <pkg>/.M0Commands --es m0 probe`).
 * Release builds disable the alias and ignore extras.
 * The model route's token is a credential for a loopback relay on the
 * developer's computer, never a provider key; it is not stored.
 */
class MainActivity : Activity() {
    private val worker = Executors.newSingleThreadExecutor()
    private lateinit var output: TextView

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        output = TextView(this).apply { setTextIsSelectable(true); textSize = 13f }
        val column = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(32, 32, 32, 32)
            addView(button("Core probe") { run("probe", null) })
            addView(button("HTTPS probe") { run("https", null) })
            addView(button("Read this screen") { run("tree", null) })
            addView(output, LinearLayout.LayoutParams(MATCH_PARENT, WRAP_CONTENT))
        }
        setContentView(ScrollView(this).apply { addView(column) })
        // Super's protected state lives in the app's private storage; the
        // model workspace is a sibling, never inside it.
        val init = Niumacore.init(File(filesDir, "super-state").apply { mkdirs() }.canonicalPath)
        show("core ${Niumacore.version()} · init $init · screen reader ${if (UiTreeService.instance != null) "on" else "off"}")
        handle(intent)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
    }

    override fun onDestroy() {
        worker.shutdown()
        super.onDestroy()
    }

    private fun handle(intent: Intent?) {
        // Only the DUMP-protected alias carries test commands; the launcher
        // entry ignores extras, so other apps cannot trigger these actions.
        if (!BuildConfig.DEBUG || intent?.component?.className != "$packageName$COMMANDS_ALIAS") return
        val action = intent.getStringExtra(EXTRA_ACTION) ?: return
        run(action, intent)
    }

    private fun run(action: String, intent: Intent?) {
        when (action) {
            "measure_on", "measure_off" -> {
                UiTreeService.measuring = action == "measure_on"
                report(action, "{\"ok\":${UiTreeService.instance != null},\"measuring\":${UiTreeService.measuring}}")
                return
            }
            "tree" -> {
                val service = UiTreeService.instance
                if (service == null) {
                    report(action, "{\"ok\":false,\"error\":\"screen reader is off\"}")
                } else {
                    service.measureNow("on request")
                    report(action, "{\"ok\":true,\"requested\":true,\"result\":\"logcat tag $TAG\"}")
                }
                return
            }
        }
        worker.execute {
            val result = when (action) {
                "probe" -> Niumacore.probe(filesDir.canonicalPath)
                "https" -> Niumacore.httpsProbe(intent?.getStringExtra("url") ?: DEFAULT_HTTPS_URL)
                "model" -> modelCall(intent)
                else -> "{\"ok\":false,\"error\":\"unknown action\"}"
            }
            report(action, result)
        }
    }

    private fun modelCall(intent: Intent?): String {
        val baseUrl = intent?.getStringExtra("base_url") ?: DEFAULT_RELAY_URL
        val model = intent?.getStringExtra("model") ?: return "{\"ok\":false,\"error\":\"model extra missing\"}"
        val token = intent.getStringExtra("token") ?: return "{\"ok\":false,\"error\":\"token extra missing\"}"
        val objective = intent.getStringExtra("objective") ?: "Reply with one short sentence confirming you received this from a phone."
        val workspace = File(filesDir, "m0-workspace").apply { mkdirs() }.canonicalPath
        val route = "{\"base_url\":${UiSnapshot.jsonString(baseUrl)},\"model\":${UiSnapshot.jsonString(model)}," +
            "\"token\":${UiSnapshot.jsonString(token)}}"
        return Niumacore.modelCall(route, workspace, objective)
    }

    private fun report(action: String, json: String) {
        Log.i(TAG, "$action $json")
        runOnUiThread { show("$action → $json") }
    }

    private fun show(line: String) {
        output.append(line + "\n\n")
    }

    private fun button(label: String, onClick: () -> Unit) = Button(this).apply {
        text = label
        setOnClickListener { onClick() }
    }

    companion object {
        const val TAG = "NiumaM0"
        private const val EXTRA_ACTION = "m0"
        private const val COMMANDS_ALIAS = ".M0Commands"
        private const val DEFAULT_RELAY_URL = "http://127.0.0.1:18080/v1"
        private const val DEFAULT_HTTPS_URL = "https://connectivitycheck.gstatic.com/generate_204"
    }
}
