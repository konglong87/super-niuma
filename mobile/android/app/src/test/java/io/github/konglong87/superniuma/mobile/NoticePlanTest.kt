package io.github.konglong87.superniuma.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class NoticePlanTest {
    private fun state(id: String, status: String, pending: String? = null, verified: Boolean? = null): TaskSnapshot {
        val p = pending?.let { ""","pending":{"id":"$it","risk":"delete","what":"tap \"Delete\"","app":"com.example.clock","intent":"x","expires_ms":5}""" } ?: ""
        val v = verified?.let { ""","verified":$it""" } ?: ""
        return TaskSnapshot.parse("""{"screen_reader":true,"task":{"id":"$id","goal":"Delete the alarm","status":"$status","steps":[]$p$v}}""")
    }

    @Test
    fun approvalAppearsThenIsWithdrawnWhenAnswered() {
        val running = state("t1", "running")
        val waiting = state("t1", "running", pending = "ap-1")
        val shown = NoticePlan.plan(running, waiting)
        assertEquals(1, shown.size)
        assertTrue(shown[0] is Notice.ShowApproval && (shown[0] as Notice.ShowApproval).approval.id == "ap-1")
        assertEquals(listOf(Notice.CancelApproval), NoticePlan.plan(waiting, state("t1", "running")))
        assertTrue("same approval again is not re-posted", NoticePlan.plan(waiting, waiting).isEmpty())
    }

    @Test
    fun aNewApprovalReplacesTheOld() {
        val plan = NoticePlan.plan(state("t1", "running", pending = "ap-1"), state("t1", "running", pending = "ap-2"))
        assertEquals(2, plan.size)
        assertEquals(Notice.CancelApproval, plan[0])
        assertTrue(plan[1] is Notice.ShowApproval)
    }

    @Test
    fun endingTaskWithdrawsApprovalAndTellsHowItEnded() {
        val plan = NoticePlan.plan(state("t1", "running", pending = "ap-1"), state("t1", "done", verified = true))
        assertEquals(Notice.CancelApproval, plan[0])
        assertEquals(Notice.ShowResult("t1", "任务完成，已验收", "Delete the alarm"), plan[1])
        for ((status, title) in listOf("needs_attention" to "任务需要你检查", "failed" to "任务失败", "interrupted" to "任务中断")) {
            assertEquals(listOf(Notice.ShowResult("t1", title, "Delete the alarm")), NoticePlan.plan(state("t1", "running"), state("t1", status)))
        }
    }

    @Test
    fun stoppedByThePersonAndUnrelatedUpdatesNeedNoNotice() {
        assertTrue(NoticePlan.plan(state("t1", "running"), state("t1", "stopped")).isEmpty())
        assertTrue(NoticePlan.plan(state("t1", "running"), state("t1", "running")).isEmpty())
        assertTrue("a finished task seen again at app start", NoticePlan.plan(null, state("t1", "done")).isEmpty())
        assertTrue(NoticePlan.plan(null, TaskSnapshot.parse("""{"screen_reader":false}""")).isEmpty())
    }

    @Test
    fun aNewTaskClearsTheLastResult() {
        assertEquals(listOf(Notice.CancelResult), NoticePlan.plan(state("t1", "done"), state("t2", "running")))
    }
}
