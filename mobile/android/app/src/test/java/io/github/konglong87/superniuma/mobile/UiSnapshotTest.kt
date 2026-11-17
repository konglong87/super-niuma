package io.github.konglong87.superniuma.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class UiSnapshotTest {
    private fun node(
        parent: Int,
        cls: String = "android.widget.TextView",
        text: String? = null,
        desc: String? = null,
        click: Boolean = false,
        edit: Boolean = false,
    ) = UiNode(parent, cls, text, desc, null, 0, 10, 100, 30, click, edit, false)

    @Test
    fun encodesTreeInTheCoreFormat() {
        val json = UiSnapshot.encode(
            "com.android.settings",
            listOf(node(-1, cls = "android.widget.FrameLayout"), node(0, text = "Wi-Fi", click = true)),
        )
        assertEquals(
            "{\"package\":\"com.android.settings\",\"nodes\":[" +
                "{\"parent\":-1,\"cls\":\"android.widget.FrameLayout\",\"b\":[0,10,100,30]}," +
                "{\"parent\":0,\"cls\":\"android.widget.TextView\",\"text\":\"Wi-Fi\",\"b\":[0,10,100,30],\"click\":true}]}",
            json,
        )
    }

    @Test
    fun escapesLabelsThatWouldBreakJson() {
        val json = UiSnapshot.encode("p", listOf(node(-1, text = "say \"hi\"\\\n\u0001 ")))
        assertTrue(json, json.contains("\"text\":\"say \\\"hi\\\"\\\\\\n\\u0001\\u2028\""))
    }

    @Test
    fun jsonStringQuotesAndEscapes() {
        assertEquals("\"http://127.0.0.1:18080/v1\"", UiSnapshot.jsonString("http://127.0.0.1:18080/v1"))
        assertEquals("\"a\\\"b\"", UiSnapshot.jsonString("a\"b"))
    }

    @Test
    fun describeKeepsOnlyUsableNodes() {
        val lines = UiSnapshot.describe(
            listOf(node(-1), node(0, text = "Network\ninternet", click = true), node(0, desc = "Search", edit = true)),
            max = 5,
        )
        assertEquals(
            listOf("[1] TextView \"Network internet\" @50,20 click", "[2] TextView \"Search\" @50,20 edit"),
            lines,
        )
    }
}
