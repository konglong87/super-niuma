package io.github.konglong87.superniuma.mobile

import android.app.Application
import android.util.Log
import io.github.konglong87.superniuma.niumacore.Listener
import io.github.konglong87.superniuma.niumacore.Niumacore
import java.io.File

/**
 * Starts the Go core before any screen or the accessibility service: the
 * service may be bound at boot without the app ever being opened.
 */
class NiumaApp : Application() {
    override fun onCreate() {
        super.onCreate()
        // Super's protected state and the task workspace are siblings in the
        // app's private storage; execution refuses a workspace inside state.
        val init = Niumacore.init(File(filesDir, "super-state").apply { mkdirs() }.canonicalPath)
        val open = Niumacore.openTasks(filesDir.canonicalPath, packageName)
        Log.i(MainActivity.TAG, "core ${Niumacore.version()} init $init tasks $open")
        Notifier.createChannels(this)
        // A new process has no pending approval (an unfinished task is now
        // interrupted): withdraw one left posted by the previous process.
        Notifier.cancel(this, Notifier.ID_APPROVAL)
        StateHub.observeTransitions { prev, next -> Notifier.apply(this, NoticePlan.plan(prev, next)) }
        Niumacore.setListener(object : Listener {
            override fun onState(stateJSON: String) = StateHub.publish(stateJSON)
        })
        StateHub.publish(Niumacore.taskState())
        ReaderWatch.checkAtStart(this)
    }
}
