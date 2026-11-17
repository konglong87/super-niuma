package io.github.konglong87.superniuma.mobile

import android.os.Handler
import android.os.Looper
import android.util.Log
import org.json.JSONObject
import java.util.concurrent.CopyOnWriteArrayList

data class PendingApproval(
    val id: String,
    val risk: String,
    val what: String,
    val app: String,
    val intent: String,
    val expiresMs: Long,
)

data class StepView(val seq: Int, val kind: String, val target: String, val outcome: String, val decision: String, val reason: String)

/** The Go core's phone.State, as the screens need it. */
data class TaskSnapshot(
    val screenReader: Boolean,
    val id: String?,
    val goal: String,
    val status: String?,
    val code: String,
    val result: String,
    val verified: Boolean?,
    val steps: List<StepView>,
    val pending: PendingApproval?,
) {
    val running: Boolean get() = status == "running"

    companion object {
        fun parse(json: String): TaskSnapshot {
            val root = JSONObject(json)
            val task = root.optJSONObject("task")
            val steps = mutableListOf<StepView>()
            task?.optJSONArray("steps")?.let { arr ->
                for (i in 0 until arr.length()) {
                    val s = arr.getJSONObject(i)
                    steps += StepView(
                        s.optInt("seq"), s.optString("kind"), s.optString("target"),
                        s.optString("outcome"), s.optString("decision"), s.optString("reason"),
                    )
                }
            }
            val pending = task?.optJSONObject("pending")?.let {
                PendingApproval(
                    it.optString("id"), it.optString("risk"), it.optString("what"),
                    it.optString("app"), it.optString("intent"), it.optLong("expires_ms"),
                )
            }
            return TaskSnapshot(
                screenReader = root.optBoolean("screen_reader"),
                id = task?.optString("id"),
                goal = task?.optString("goal") ?: "",
                status = task?.optString("status"),
                code = task?.optString("code") ?: "",
                result = task?.optString("result") ?: "",
                verified = if (task != null && task.has("verified")) task.optBoolean("verified") else null,
                steps = steps,
                pending = pending,
            )
        }

        /**
         * Log lines describing what changed from [prev] to [next]: a new
         * task status, a new pending approval, and each new step. Test
         * scripts read them; they never contain the model key.
         */
        fun changes(prev: TaskSnapshot?, next: TaskSnapshot): List<String> {
            val lines = mutableListOf<String>()
            if (next.id == null) return lines
            val sameTask = prev?.id == next.id
            val seen = if (sameTask) prev!!.steps.maxOfOrNull { it.seq } ?: 0 else 0
            for (s in next.steps.filter { it.seq > seen }) {
                lines += "step " + JSONObject()
                    .put("task", next.id).put("seq", s.seq).put("kind", s.kind).put("target", s.target)
                    .put("outcome", s.outcome).put("decision", s.decision).put("reason", s.reason).toString()
            }
            val p = next.pending
            if (p != null && (!sameTask || prev!!.pending?.id != p.id)) {
                lines += "approval " + JSONObject()
                    .put("task", next.id).put("id", p.id).put("risk", p.risk).put("what", p.what).put("app", p.app).toString()
            }
            if (!sameTask || prev!!.status != next.status) {
                val o = JSONObject().put("id", next.id).put("status", next.status).put("code", next.code)
                if (next.verified != null) o.put("verified", next.verified)
                if (!next.running) o.put("result", next.result)
                lines += "task $o"
            }
            return lines
        }
    }
}

/**
 * Receives the core's state from any thread, logs what changed under
 * [MainActivity.TAG], and hands the newest state to observers on the main
 * thread.
 */
object StateHub {
    private val main = Handler(Looper.getMainLooper())
    private val observers = CopyOnWriteArrayList<(TaskSnapshot) -> Unit>()
    private var last: TaskSnapshot? = null

    @Volatile
    var latest: TaskSnapshot? = null
        private set

    @Synchronized
    fun publish(json: String) {
        val next = try {
            TaskSnapshot.parse(json)
        } catch (e: Exception) {
            Log.w(MainActivity.TAG, "unreadable state from core")
            return
        }
        TaskSnapshot.changes(last, next).forEach { Log.i(MainActivity.TAG, it) }
        last = next
        latest = next
        main.post { observers.forEach { it(next) } }
    }

    fun observe(observer: (TaskSnapshot) -> Unit) {
        observers += observer
        latest?.let { s -> main.post { observer(s) } }
    }

    fun forget(observer: (TaskSnapshot) -> Unit) {
        observers -= observer
    }
}
