package io.github.konglong87.superniuma.mobile

import android.content.ComponentName
import android.content.Context
import android.provider.Settings
import android.util.Log

/**
 * Notices the screen reader going off. When the person or the system turns
 * it off while the app runs, the service's unbind tells us at once. A force
 * stop kills the process instead and Android also turns the service off;
 * nothing of the app can run until it is opened again, so that case is
 * caught when the app next starts.
 */
object ReaderWatch {
    private const val PREFS = "reader_watch"
    private const val EXPECTED = "expected_on"

    /** The service connected: remember the person wants it on. */
    fun connected(ctx: Context) {
        prefs(ctx).edit().putBoolean(EXPECTED, true).apply()
        Notifier.cancel(ctx, Notifier.ID_READER)
    }

    /** The service was unbound while the app runs. */
    fun disconnected(ctx: Context) {
        if (enabled(ctx)) return // a restart of the service, not the person turning it off
        remind(ctx, "unbound")
    }

    /** At app start: the reader was on before and is off now (e.g. after a force stop). */
    fun checkAtStart(ctx: Context) {
        if (prefs(ctx).getBoolean(EXPECTED, false) && !enabled(ctx)) remind(ctx, "app_start")
    }

    /** Whether the service is enabled in the system settings. */
    fun enabled(ctx: Context): Boolean {
        val mine = ComponentName(ctx, UiTreeService::class.java)
        val list = Settings.Secure.getString(ctx.contentResolver, Settings.Secure.ENABLED_ACCESSIBILITY_SERVICES) ?: return false
        return list.split(':').any { ComponentName.unflattenFromString(it) == mine }
    }

    /** One reminder per time it goes off: it is not repeated until the reader comes back. */
    private fun remind(ctx: Context, why: String) {
        prefs(ctx).edit().putBoolean(EXPECTED, false).apply()
        Notifier.showReaderOff(ctx)
        Log.i(MainActivity.TAG, "reader {\"on\":false,\"reminded\":\"$why\"}")
    }

    private fun prefs(ctx: Context) = ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
}
