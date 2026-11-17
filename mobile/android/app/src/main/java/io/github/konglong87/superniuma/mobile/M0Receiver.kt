package io.github.konglong87.superniuma.mobile

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import org.json.JSONObject

/**
 * Test-script queries that must not open an activity (debug builds only;
 * the manifest requires DUMP, which only adb shell holds):
 *
 *   am broadcast -n <pkg>/.M0Receiver --es m0 locate --es text 允许一次 --es pkg com.android.systemui
 *
 * logs `locate {"text":...,"x":...,"y":...}` so the script can press the
 * button with a real touch, as a person would.
 */
class M0Receiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (!BuildConfig.DEBUG || intent.getStringExtra("m0") != "locate") return
        val text = intent.getStringExtra("text") ?: return
        val pkg = intent.getStringExtra("pkg")
        val at = UiTreeService.instance?.locate(text, pkg)
        val o = JSONObject().put("text", text).put("pkg", pkg ?: "").put("found", at != null)
        if (at != null) o.put("x", at[0]).put("y", at[1])
        Log.i(MainActivity.TAG, "locate $o")
    }
}
