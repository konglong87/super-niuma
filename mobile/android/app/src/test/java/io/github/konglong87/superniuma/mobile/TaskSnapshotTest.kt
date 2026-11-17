package io.github.konglong87.superniuma.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class TaskSnapshotTest {
    private val running = """{"screen_reader":true,"task":{"id":"t1","goal":"Set an alarm","status":"running",
        "steps":[{"seq":1,"kind":"screen","outcome":"done"},{"seq":2,"kind":"tap","target":"Delete","outcome":"denied","decision":"denied"}],
        "pending":{"id":"ap-1","risk":"delete","what":"tap \"Delete\"","app":"com.example.clock","intent":"remove it","expires_ms":5}}}"""

    @Test
    fun parsesTaskStepsAndPending() {
        val s = TaskSnapshot.parse(running)
        assertTrue(s.screenReader)
        assertTrue(s.running)
        assertEquals("t1", s.id)
        assertEquals(2, s.steps.size)
        assertEquals("Delete", s.steps[1].target)
        assertEquals("ap-1", s.pending?.id)
        assertNull(s.verified)
    }

    @Test
    fun parsesAndLogsEachRetryWaitOnce() {
        val waiting = TaskSnapshot.parse(
            """{"screen_reader":true,"task":{"id":"t1","goal":"g","status":"running","steps":[],
            "retry":{"retry":1,"max":3,"wait_ms":7000,"until_ms":100,"code":"execution_http_rate_limited"}}}""",
        )
        assertEquals(RetryWait(1, 3, 7000, 100, "execution_http_rate_limited"), waiting.retry)
        assertEquals("模型服务限流", waiting.retry?.reason)
        val running = TaskSnapshot.parse("""{"screen_reader":true,"task":{"id":"t1","goal":"g","status":"running","steps":[]}}""")
        val lines = TaskSnapshot.changes(running, waiting)
        assertEquals(1, lines.size)
        assertTrue(lines[0], lines[0].startsWith("retry ") && lines[0].contains("\"wait_ms\":7000"))
        assertTrue(TaskSnapshot.changes(waiting, waiting).isEmpty())
    }

    @Test
    fun parsesEmptyState() {
        val s = TaskSnapshot.parse("""{"screen_reader":false}""")
        assertNull(s.id)
        assertFalse(s.running)
        assertTrue(TaskSnapshot.changes(null, s).isEmpty())
    }

    @Test
    fun changesReportNewStepsApprovalAndStatusOnce() {
        val first = TaskSnapshot.parse(running)
        val lines = TaskSnapshot.changes(null, first)
        assertEquals(4, lines.size)
        assertTrue(lines[0], lines[0].startsWith("step ") && lines[0].contains("\"seq\":1"))
        assertTrue(lines[2], lines[2].startsWith("approval ") && lines[2].contains("\"id\":\"ap-1\""))
        assertTrue(lines[3], lines[3].startsWith("task ") && lines[3].contains("\"status\":\"running\""))
        assertTrue(TaskSnapshot.changes(first, first).isEmpty())

        val done = TaskSnapshot.parse(
            """{"screen_reader":true,"task":{"id":"t1","goal":"Set an alarm","status":"done","verified":true,"result":"Alarm set.",
            "steps":[{"seq":1,"kind":"screen","outcome":"done"},{"seq":2,"kind":"tap","outcome":"denied"},{"seq":3,"kind":"tap","outcome":"done"}]}}""",
        )
        val next = TaskSnapshot.changes(first, done)
        assertEquals(2, next.size)
        assertTrue(next[0], next[0].contains("\"seq\":3"))
        assertTrue(next[1], next[1].contains("\"verified\":true") && next[1].contains("Alarm set."))
    }
}
