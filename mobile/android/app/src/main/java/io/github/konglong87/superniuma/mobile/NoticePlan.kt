package io.github.konglong87.superniuma.mobile

/** What the notifications should do after a state change. Plain data, so the decisions are testable on the JVM. */
sealed class Notice {
    data class ShowApproval(val taskId: String, val approval: PendingApproval) : Notice()
    object CancelApproval : Notice()
    data class ShowResult(val taskId: String, val title: String, val goal: String) : Notice()
    object CancelResult : Notice()
}

object NoticePlan {
    /**
     * The notices for a change from [prev] to [next]: an approval appearing
     * is shown and one that is answered, expires or ends with the task is
     * withdrawn; a task that ends tells the person how, unless the person
     * stopped it themselves; a new task clears the last result.
     */
    fun plan(prev: TaskSnapshot?, next: TaskSnapshot): List<Notice> {
        val id = next.id ?: return emptyList()
        val sameTask = prev?.id == id
        val out = mutableListOf<Notice>()
        val before = if (sameTask) prev?.pending else null
        val now = if (next.running) next.pending else null
        if (before != null && before.id != now?.id) out += Notice.CancelApproval
        if (now != null && now.id != before?.id) out += Notice.ShowApproval(id, now)
        if (!sameTask && next.running) out += Notice.CancelResult
        val ended = sameTask && prev?.running == true && !next.running
        if (ended) resultTitle(next)?.let { out += Notice.ShowResult(id, it, next.goal) }
        return out
    }

    /** The title for a finished task, or null when no notice is needed. */
    fun resultTitle(s: TaskSnapshot): String? = when (s.status) {
        "done" -> if (s.verified == true) "任务完成，已验收" else "任务完成（未设置验收文字）"
        "needs_attention" -> "任务需要你检查"
        "failed" -> "任务失败"
        "interrupted" -> "任务中断"
        else -> null // stopped: the person did it and knows
    }
}
