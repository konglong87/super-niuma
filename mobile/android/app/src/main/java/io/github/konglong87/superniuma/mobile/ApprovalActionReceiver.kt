package io.github.konglong87.superniuma.mobile

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log
import io.github.konglong87.superniuma.niumacore.Niumacore
import org.json.JSONObject

/**
 * Receives Allow once / Deny from the approval notification. It is not
 * exported: only the app's own immutable PendingIntents reach it. The core
 * accepts an answer only for the approval that is still pending, so a late
 * or repeated tap changes nothing.
 */
class ApprovalActionReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action != ACTION) return
        val id = intent.getStringExtra(EXTRA_ID) ?: return
        val decision = intent.getStringExtra(EXTRA_DECISION) ?: return
        if (decision != "allow_once" && decision != "deny") return
        val result = Niumacore.decide(id, decision)
        Notifier.cancel(context, Notifier.ID_APPROVAL)
        // The shade covers the app the step runs in; the core waits a few
        // seconds for that app to be in front again before acting.
        UiTreeService.instance?.dismissShade()
        Log.i(MainActivity.TAG, "decide " + JSONObject().put("via", "notification").put("id", id).put("decision", decision).put("result", JSONObject(result)))
    }

    companion object {
        const val ACTION = "io.github.konglong87.superniuma.mobile.APPROVAL_DECISION"
        const val EXTRA_ID = "approval_id"
        const val EXTRA_DECISION = "decision"
    }
}
