package io.github.konglong87.superniuma.mobile

/**
 * One element of a screen as the accessibility service sees it. [parent] is
 * the index of the parent element in the same snapshot, -1 for a window root.
 * Plain data, with no Android types, so the encoding is testable on the JVM.
 */
data class UiNode(
    val parent: Int,
    val cls: String,
    val text: String?,
    val desc: String?,
    val viewId: String?,
    val left: Int,
    val top: Int,
    val right: Int,
    val bottom: Int,
    val clickable: Boolean,
    val editable: Boolean,
    val scrollable: Boolean,
    val password: Boolean = false,
    val checked: Boolean = false,
)

/** The snapshot format the Go core reads (phone.Snapshot). */
object UiSnapshot {
    /** [home] is the package of the phone's home screen, when known. */
    fun encode(packageName: String, nodes: List<UiNode>, home: String? = null): String {
        val out = StringBuilder(64 + nodes.size * 96)
        out.append("{\"package\":")
        appendString(out, packageName)
        if (!home.isNullOrEmpty()) {
            out.append(",\"home\":")
            appendString(out, home)
        }
        out.append(",\"nodes\":[")
        nodes.forEachIndexed { i, n ->
            if (i > 0) out.append(',')
            out.append("{\"parent\":").append(n.parent).append(",\"cls\":")
            appendString(out, n.cls)
            if (!n.text.isNullOrEmpty()) {
                out.append(",\"text\":")
                appendString(out, n.text)
            }
            if (!n.desc.isNullOrEmpty()) {
                out.append(",\"desc\":")
                appendString(out, n.desc)
            }
            if (!n.viewId.isNullOrEmpty()) {
                out.append(",\"rid\":")
                appendString(out, n.viewId)
            }
            out.append(",\"b\":[").append(n.left).append(',').append(n.top).append(',')
                .append(n.right).append(',').append(n.bottom).append(']')
            if (n.clickable) out.append(",\"click\":true")
            if (n.editable) out.append(",\"edit\":true")
            if (n.scrollable) out.append(",\"scroll\":true")
            if (n.password) out.append(",\"pwd\":true")
            if (n.checked) out.append(",\"checked\":true")
            out.append('}')
        }
        out.append("]}")
        return out.toString()
    }

    /** A few human-readable lines for logs: index, short class, label, centre. */
    fun describe(nodes: List<UiNode>, max: Int): List<String> =
        nodes.withIndex()
            .filter { (_, n) ->
                // Same notion of "usable" as the core: labelled or actionable.
                !n.text.isNullOrBlank() || !n.desc.isNullOrBlank() || n.clickable || n.editable || n.scrollable
            }
            .take(max)
            .map { (i, n) ->
                val label = (n.text?.takeIf { it.isNotBlank() } ?: n.desc ?: "").replace('\n', ' ').take(40)
                val kind = n.cls.substringAfterLast('.')
                val flags = buildString {
                    if (n.clickable) append(" click")
                    if (n.editable) append(" edit")
                    if (n.scrollable) append(" scroll")
                }
                "[$i] $kind \"$label\" @${(n.left + n.right) / 2},${(n.top + n.bottom) / 2}$flags"
            }

    /** [s] as a JSON string literal, quotes included. */
    fun jsonString(s: String): String = StringBuilder(s.length + 2).also { appendString(it, s) }.toString()

    private fun appendString(out: StringBuilder, s: String) {
        out.append('"')
        for (c in s) {
            when {
                c == '"' -> out.append("\\\"")
                c == '\\' -> out.append("\\\\")
                c == '\n' -> out.append("\\n")
                c == '\r' -> out.append("\\r")
                c == '\t' -> out.append("\\t")
                c < ' ' || c == ' ' || c == ' ' ->
                    out.append("\\u").append(String.format(java.util.Locale.ROOT, "%04x", c.code))
                else -> out.append(c)
            }
        }
        out.append('"')
    }
}
