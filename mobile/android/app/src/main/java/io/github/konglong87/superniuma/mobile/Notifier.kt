package io.github.konglong87.superniuma.mobile

import android.Manifest
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.ComponentName
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.graphics.drawable.Icon
import android.os.Build
import android.provider.Settings
import android.util.Log

/**
 * System notifications for when the person is not looking at the screen:
 * a step waiting for approval (with Allow once / Deny), how a task ended,
 * and the screen reader being turned off. Overlay and in-app controls keep
 * working when notifications are not allowed.
 */
object Notifier {
    const val CH_APPROVALS = "approvals"
    const val CH_TASKS = "tasks"
    const val CH_READER = "reader"
    const val ID_APPROVAL = 1001
    const val ID_RESULT = 1002
    const val ID_READER = 1003

    fun createChannels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannel(
            NotificationChannel(CH_APPROVALS, "需要你确认的步骤", NotificationManager.IMPORTANCE_HIGH)
                .apply { description = "发送、付款、删除等步骤在执行前等你允许或拒绝" },
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_TASKS, "任务结果", NotificationManager.IMPORTANCE_DEFAULT)
                .apply { description = "任务完成、需要检查、失败或中断" },
        )
        nm.createNotificationChannel(
            NotificationChannel(CH_READER, "屏幕助手状态", NotificationManager.IMPORTANCE_HIGH)
                .apply { description = "屏幕助手被关闭时提醒你重新打开" },
        )
    }

    /** Whether the app may post notifications at all. */
    fun allowed(ctx: Context): Boolean {
        val granted = Build.VERSION.SDK_INT < Build.VERSION_CODES.TIRAMISU ||
            ctx.checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
        return granted && ctx.getSystemService(NotificationManager::class.java).areNotificationsEnabled()
    }

    fun apply(ctx: Context, notices: List<Notice>) {
        for (n in notices) {
            when (n) {
                is Notice.ShowApproval -> showApproval(ctx, n)
                Notice.CancelApproval -> cancel(ctx, ID_APPROVAL)
                is Notice.ShowResult -> showResult(ctx, n)
                Notice.CancelResult -> cancel(ctx, ID_RESULT)
            }
        }
    }

    fun cancel(ctx: Context, id: Int) = ctx.getSystemService(NotificationManager::class.java).cancel(id)

    private fun showApproval(ctx: Context, n: Notice.ShowApproval) {
        val p = n.approval
        val left = (p.expiresMs - System.currentTimeMillis()).coerceAtLeast(1_000)
        val allow = Notification.Action.Builder(
            Icon.createWithResource(ctx, android.R.drawable.ic_menu_send), "允许一次", decision(ctx, p.id, "allow_once"),
        )
        // Allowing a risky step from the lock screen asks for the screen lock first.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) allow.setAuthenticationRequired(true)
        val deny = Notification.Action.Builder(
            Icon.createWithResource(ctx, android.R.drawable.ic_menu_close_clear_cancel), "拒绝", decision(ctx, p.id, "deny"),
        )
        val body = "牛马想要：${p.what}\n应用：${p.app}\n理由：${p.intent.ifBlank { "（未说明）" }}\n${left / 1000} 秒内不处理将自动拒绝。"
        // The lock screen shows only that something waits, never what.
        val public = Notification.Builder(ctx, CH_APPROVALS)
            .setSmallIcon(android.R.drawable.ic_dialog_alert)
            .setContentTitle("超级牛马有一步需要你确认")
            .build()
        post(
            ctx, ID_APPROVAL,
            Notification.Builder(ctx, CH_APPROVALS)
                .setSmallIcon(android.R.drawable.ic_dialog_alert)
                .setContentTitle("需要你确认")
                .setContentText("牛马想要：${p.what}")
                .setStyle(Notification.BigTextStyle().bigText(body))
                .setCategory(Notification.CATEGORY_REMINDER)
                .setVisibility(Notification.VISIBILITY_PRIVATE)
                .setPublicVersion(public)
                .setTimeoutAfter(left)
                .setOnlyAlertOnce(true)
                .setContentIntent(openApp(ctx))
                .addAction(deny.build())
                .addAction(allow.build())
                .build(),
        )
    }

    private fun showResult(ctx: Context, n: Notice.ShowResult) = post(
        ctx, ID_RESULT,
        Notification.Builder(ctx, CH_TASKS)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle(n.title)
            .setContentText(n.goal)
            .setVisibility(Notification.VISIBILITY_PRIVATE)
            .setContentIntent(openApp(ctx))
            .setAutoCancel(true)
            .build(),
    )

    /** The screen reader went off: one tap leads to the accessibility settings. */
    fun showReaderOff(ctx: Context) = post(
        ctx, ID_READER,
        Notification.Builder(ctx, CH_READER)
            .setSmallIcon(android.R.drawable.ic_dialog_alert)
            .setContentTitle("超级牛马的屏幕助手已关闭")
            .setContentText("任务需要它才能运行。点此打开无障碍设置，重新开启“超级牛马 · 屏幕助手”。")
            .setStyle(Notification.BigTextStyle().bigText("任务需要它才能运行。点此打开无障碍设置，重新开启“超级牛马 · 屏幕助手”。强行停止应用也会让系统关掉它。"))
            .setContentIntent(
                PendingIntent.getActivity(
                    ctx, 3, Intent(Settings.ACTION_ACCESSIBILITY_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
                ),
            )
            .setAutoCancel(true)
            .build(),
    )

    private fun post(ctx: Context, id: Int, n: Notification) {
        if (!allowed(ctx)) {
            Log.i(MainActivity.TAG, "notice skipped {\"id\":$id,\"reason\":\"notifications not allowed\"}")
            return
        }
        ctx.getSystemService(NotificationManager::class.java).notify(id, n)
        Log.i(MainActivity.TAG, "notice {\"id\":$id}")
    }

    private fun openApp(ctx: Context) = PendingIntent.getActivity(
        ctx, 0, Intent(ctx, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )

    /** An explicit, immutable broadcast to the app's own non-exported receiver. */
    private fun decision(ctx: Context, approvalId: String, decision: String) = PendingIntent.getBroadcast(
        ctx, if (decision == "allow_once") 1 else 2,
        Intent(ApprovalActionReceiver.ACTION)
            .setComponent(ComponentName(ctx, ApprovalActionReceiver::class.java))
            .putExtra(ApprovalActionReceiver.EXTRA_ID, approvalId)
            .putExtra(ApprovalActionReceiver.EXTRA_DECISION, decision),
        PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
    )
}
