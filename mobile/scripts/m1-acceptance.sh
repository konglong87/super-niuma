#!/usr/bin/env bash
# M1 end-to-end acceptance on an Android emulator with a real model.
#
#   mobile/scripts/m1-acceptance.sh <provider-name> <evidence-dir>
#
# <provider-name> is an openai-chat-completions entry of ~/.niuma/settings.json;
# the phone reaches it through mobile/scripts/dev-model-relay.py over
# `adb reverse`, so the key stays on this computer. Needs a booted emulator
# (ANDROID_SERIAL), a debug and a release build of mobile/android, jq, python3.
# It resets the Clock app's data and changes accessibility settings on the
# device: use a test emulator. Approvals and Stop are pressed with real
# touches on the overlay, at the positions the debug build logs.
set -u
PROVIDER=${1:?provider name}
OUT=${2:?evidence directory}
REPO=$(cd "$(dirname "$0")/../.." && pwd)
APK=$REPO/mobile/android/app/build/outputs/apk/debug/app-debug.apk
REL=$REPO/mobile/android/app/build/outputs/apk/release/app-release-unsigned.apk
AAPT2=$(ls -d "$ANDROID_HOME"/build-tools/*/aapt2 | tail -1)
P=io.github.konglong87.superniuma.mobile
CLOCK=com.google.android.deskclock
COOLDOWN=${COOLDOWN:-75}
mkdir -p "$OUT"
pass=0; fail=0
ok() { echo "PASS  $1" | tee -a "$OUT/result.txt"; pass=$((pass+1)); }
bad() { echo "FAIL  $1" | tee -a "$OUT/result.txt"; fail=$((fail+1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
note() { echo "      $*" | tee -a "$OUT/result.txt"; }
logs() { adb logcat -d -s 'NiumaM0:*' | sed -n 's/.*NiumaM0 : //p'; }
cmd() {
	local s="am start -n $P/.M0Commands --es m0" a q="'"
	for a in "$@"; do s+=" '${a//$q/$q\\$q$q}'"; done
	adb shell "$s" >/dev/null 2>&1
}
# Only lines of the task this scenario started count (TASK_ID from its start).
TASK_ID=
last_task() { logs | grep '^task {' | grep -F "\"id\":\"${TASK_ID}" | tail -1 | cut -d' ' -f2-; }
wait_task() { # seconds; prints the final task line
	for _ in $(seq 1 "$1"); do
		local t; t=$(last_task)
		if [ -n "$t" ] && ! echo "$t" | grep -q '"status":"running"'; then echo "$t"; return 0; fi
		sleep 1
	done
	last_task; return 1
}
wait_line() { for _ in $(seq 1 "$2"); do logs | grep -q "$1" && return 0; sleep 1; done; return 1; }
press() { # allow | deny | stop: a real touch where the overlay button is
	local at; at=$(logs | grep '^overlay {' | grep "\"$1\"" | tail -1 | cut -d' ' -f2- | jq -r ".$1 | \"\(.[0]) \(.[1])\"")
	[ -n "$at" ] && [ "$at" != "null null" ] && adb shell input tap $at
}
# Notifications of the app as `dumpsys notification` lists them.
notices() { adb shell dumpsys notification --noredact | tr -d '\r'; }
notice_posted() { notices | grep -q "pkg=$P user=UserHandle{0} id=$1 "; }
notice_titled() { notices | grep -A 30 "pkg=$P user=UserHandle{0} id=$1 " | grep -qF "android.title=String ($2)"; }
# press_notification allow|deny: opens the shade and touches the button of
# the approval notification, found by the app's debug-only locate query.
press_notification() {
	local text=允许一次 at
	[ "$1" = deny ] && text=拒绝
	adb shell cmd statusbar expand-notifications; sleep 2
	adb shell am broadcast -n $P/.M0Receiver --es m0 locate --es text "$text" --es pkg com.android.systemui >/dev/null
	sleep 1
	at=$(logs | grep '^locate {' | tail -1 | cut -d' ' -f2- | jq -r 'select(.found) | "\(.x) \(.y)"')
	[ -n "$at" ] && adb shell input tap $at
}
answer_all() { # allow|deny seconds [notification]
	local seen=0 n
	for _ in $(seq 1 "$2"); do
		n=$(logs | grep '^approval {' | grep -cF "\"task\":\"${TASK_ID}")
		if [ "$n" -gt "$seen" ]; then
			sleep 1.5
			# The first card of a run is kept as evidence when CARD_SHOT names it.
			[ -n "${CARD_SHOT:-}" ] && { shot "$CARD_SHOT"; CARD_SHOT=; }
			if [ "${3:-}" = notification ]; then
				# Kept for the checks: how the approval notification was posted.
				notices | grep -A 30 "pkg=$P user=UserHandle{0} id=1001 " > "$OUT/.approval-notice"
				press_notification "$1"
			else
				press "$1"
			fi
			seen=$n
		fi
		last_task | grep -q '"status":"running"' || [ -z "$(last_task)" ] || break
		sleep 1
	done
}
steps() { logs | grep '^step {' | cut -d' ' -f2- | jq -c '{seq, kind, target, outcome, decision}' | tee -a "$OUT/steps.txt" | sed 's/^/      /'; }
alarm_shown() { # "7:00": an alarm row for that time, AM. Android 14 puts a
	# narrow no-break space (U+202F) between the time and AM, so match any
	# short separator.
	adb shell am start -a android.intent.action.SHOW_ALARMS -p $CLOCK >/dev/null 2>&1; sleep 3
	adb shell uiautomator dump /sdcard/ui.xml >/dev/null 2>&1
	adb shell cat /sdcard/ui.xml | LC_ALL=C grep -Eq "content-desc=\"$1[^\"]{1,4}AM"
}
logs_all_scope_denied() {
	echo "$S" | jq -e "[.task.steps[] | select(.kind==\"launch\" and .target!=\"$CLOCK\" and (.reason|tostring|startswith(\"scope_denied\")|not))] | length == 0" >/dev/null
}
shot() { adb exec-out screencap -p > "$OUT/$1.png"; }
fresh_clock() { adb shell pm clear $CLOCK >/dev/null; adb shell input keyevent KEYCODE_HOME; }
start_task() { # name goal [expect]
	echo "== $1" | tee -a "$OUT/result.txt"
	adb shell input keyevent KEYCODE_HOME; adb logcat -c
	echo "--- $1" >> "$OUT/steps.txt"
	if [ -n "${3:-}" ]; then cmd m1_start --es goal "$2" --es apps $CLOCK --es expect "$3"; else cmd m1_start --es goal "$2" --es apps $CLOCK; fi
	TASK_ID=
	for _ in $(seq 1 30); do
		TASK_ID=$(logs | grep '^start {"ok":true' | tail -1 | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
		[ -n "$TASK_ID" ] && break
		sleep 1
	done
}
# task_run name goal expect allow|deny|none: runs one task; a provider problem
# (HTTP 429 or a truncated reply) is retried once after a cooldown and noted.
task_run() {
	local attempt
	for attempt in 1 2; do
		start_task "$1" "$2" "$3"
		case $4 in
		allow | deny) answer_all "$4" 300 ;;
		allow-notification | deny-notification) answer_all "${4%-notification}" 300 notification ;;
		esac
		T=$(wait_task 300)
		if [ "$attempt" = 1 ] && echo "$T" | grep -Eq 'rate_limited|output_truncated'; then
			note "provider problem ($(echo "$T" | jq -r .code)); retrying once after ${COOLDOWN}s"
			sleep "$COOLDOWN"
			continue
		fi
		break
	done
	note "$T" | cut -c1-300
	steps >/dev/null
}

: > "$OUT/result.txt"; : > "$OUT/steps.txt"
echo "M1 acceptance $(date '+%Y-%m-%d %H:%M') provider=$PROVIDER device=$(adb shell getprop ro.product.model | tr -d '\r') android=$(adb shell getprop ro.build.version.release | tr -d '\r')" | tee -a "$OUT/result.txt"

echo "== install" | tee -a "$OUT/result.txt"
adb uninstall $P >/dev/null 2>&1
check "fresh install of the debug APK" '[ "$(adb install -g "$APK" 2>&1 | tail -1)" = Success ]'
echo "== model route" | tee -a "$OUT/result.txt"
# The relay's test token lives outside the evidence directory.
TOKEN_FILE=$(mktemp)
(umask 077; openssl rand -hex 16 > "$TOKEN_FILE")
RELAY=
start_relay() { # [--inject STATUS:EVERY[:RETRY_AFTER]]...; appends to relay.log
	[ -n "$RELAY" ] && kill "$RELAY" 2>/dev/null && sleep 1
	RELAY_TEST_TOKEN=$(cat "$TOKEN_FILE") nohup python3 -I "$REPO/mobile/scripts/dev-model-relay.py" "$PROVIDER" 18080 "$@" >> "$OUT/relay.log" 2>&1 &
	RELAY=$!
	for _ in $(seq 1 20); do [ "$(grep -c "relay ready" "$OUT/relay.log")" -gt "$(cat "$OUT/.relay-count" 2>/dev/null || echo 0)" ] && break; sleep 0.5; done
	grep -c "relay ready" "$OUT/relay.log" > "$OUT/.relay-count"
}
trap 'kill $RELAY 2>/dev/null; adb reverse --remove-all >/dev/null 2>&1; rm -f "$TOKEN_FILE" "$OUT/.relay-count" "$OUT/.approval-notice"' EXIT
start_relay
adb reverse tcp:18080 tcp:18080 >/dev/null
MODEL=$(jq -r --arg n "$PROVIDER" '.fallback.providers[] | select(.name==$n) | .model' ~/.niuma/settings.json)
adb logcat -c
cmd m1_route --es model "$MODEL" --es token "$(cat "$TOKEN_FILE")"
check "route saved and read back through the Keystore" 'wait_line "^m1_route {\"ok\":true}" 30'
logs | grep -q '^m1_route {"ok":true}' || note "app log: $(logs | head -5 | tr '\n' ' ' | cut -c1-300)"

echo "== screen reader" | tee -a "$OUT/result.txt"
adb logcat -c
adb shell settings put secure enabled_accessibility_services null
adb shell am start -n $P/.MainActivity >/dev/null 2>&1; sleep 3
shot reader-off
cmd m1_start --es goal "设一个闹钟" --es apps $CLOCK; sleep 3
check "a task is refused while the screen reader is off" 'logs | grep -q "^start {\"ok\":false,\"error\":\"screen reader is off\"}"'
adb shell settings put secure enabled_accessibility_services $P/$P.UiTreeService
adb shell settings put secure accessibility_enabled 1
check "screen reader connects to the core" 'wait_line "^service connected" 20'


fresh_clock
task_run "set an alarm (verified)" "设一个早上 7:00 的闹钟" "7:00" none
check "alarm task done and verified" 'echo "$T" | jq -e ".status==\"done\" and .verified==true" >/dev/null'
check "7:00 AM alarm exists (positive control for the checks below)" 'alarm_shown "7:00"'
check "the result was notified: 任务完成，已验收" 'notice_titled 1002 "任务完成，已验收"'
shot alarm-set

sleep "$COOLDOWN"
task_run "delete, person denies from the notification" "删除早上 7:00 的闹钟" "" deny-notification
check "approval notification: private on the lock screen, two actions" 'grep -q "vis=PRIVATE publicVersion=" "$OUT/.approval-notice" && grep -q "actions=2" "$OUT/.approval-notice" && grep -qF "android.title=String (需要你确认)" "$OUT/.approval-notice"'
check "denied from the notification" 'logs | grep -q "^decide {\"via\":\"notification\".*\"decision\":\"deny\".*\"ok\":true"'
check "the delete step waited for approval" 'logs | grep -q "^approval {.*\"risk\":\"delete\""'
check "denied: task needs attention" 'echo "$T" | jq -e ".status==\"needs_attention\"" >/dev/null'
check "denied: 7:00 AM alarm still exists" 'alarm_shown "7:00"'
shot alarm-kept

sleep "$COOLDOWN"
CARD_SHOT=approval-card
task_run "delete, person allows" "删除早上 7:00 的闹钟" "" allow
check "allowed: the approved delete step ran" 'logs | grep -q "^step {.*\"decision\":\"approved\",\"reason\":\"\"" || logs | grep -q "\"outcome\":\"done\",\"decision\":\"approved\""'
check "allowed: 7:00 AM alarm is gone" '! alarm_shown "7:00" && alarm_shown "8:30"'
shot alarm-deleted

sleep "$COOLDOWN"
# A new 7:00 alarm, set by the test itself, for a delete approved from the shade.
adb shell am start -a android.intent.action.SET_ALARM --ei android.intent.extra.alarm.HOUR 7 --ei android.intent.extra.alarm.MINUTES 0 --ez android.intent.extra.alarm.SKIP_UI true >/dev/null 2>&1; sleep 2
CARD_SHOT=approval-notification
task_run "delete, person allows from the notification" "删除早上 7:00 的闹钟" "" allow-notification
check "allowed from the notification" 'logs | grep -q "^decide {\"via\":\"notification\".*\"decision\":\"allow_once\".*\"ok\":true"'
check "the step approved from the shade ran once the app was back" 'logs | grep -q "\"outcome\":\"done\",\"decision\":\"approved\""'
check "allowed from the notification: 7:00 AM alarm is gone" '! alarm_shown "7:00" && alarm_shown "8:30"'

sleep "$COOLDOWN"
fresh_clock
start_task "stop from the overlay" "设一个早上 6:30 的闹钟，并把标签设为 起床" "6:30"
wait_line '^step {"task":"[^"]*","seq":2' 120; sleep 1; shot running-pill; press stop
T=$(wait_task 60); note "$T" | cut -c1-200
before=$(adb logcat -d | grep -c "NiumaM0 : perform "); sleep 8; after=$(adb logcat -d | grep -c "NiumaM0 : perform ")
check "stopped by the person" 'echo "$T" | jq -e ".status==\"stopped\"" >/dev/null'
check "no action after stop" '[ "$before" = "$after" ]'

sleep "$COOLDOWN"
fresh_clock
start_task "app process killed mid-task" "设一个早上 6:30 的闹钟" "6:30"
wait_line '^step {"task":"[^"]*","seq":2' 120
adb shell run-as $P kill -9 "$(adb shell pidof $P | tr -d '\r')"; sleep 6
adb logcat -c; cmd m1_state; sleep 2
S=$(logs | grep '^m1_state' | cut -d' ' -f2-)
note "$(echo "$S" | jq -c '.task | {status, code, steps: (.steps|length)}')"
check "killed task is interrupted, steps kept, not retried" 'echo "$S" | jq -e ".task.status==\"interrupted\" and (.task.steps|length) >= 2" >/dev/null'

sleep "$COOLDOWN"
task_run "out-of-scope app" "打开系统设置，看看 Wi-Fi 是否开启" "" none
adb logcat -c; cmd m1_state; sleep 2
S=$(logs | grep '^m1_state' | cut -d' ' -f2-)
# The model may try another app (refused) or give up on its own; either way
# nothing may run inside an app outside the task, and a refusal says why.
HOME_PKG=$(adb shell cmd package resolve-activity --brief -c android.intent.category.HOME -a android.intent.action.MAIN | tail -1 | cut -d/ -f1 | tr -d '\r')
check "no tap, type or swipe ran inside an app outside the task" 'echo "$S" | jq -e --arg home "$HOME_PKG" "[.task.steps[] | select((.kind==\"tap\" or .kind==\"type\" or .kind==\"swipe\") and .outcome==\"done\" and .app!=\"$CLOCK\" and .app!=\$home)] | length == 0" >/dev/null'
check "any attempt outside the task was refused as scope_denied" '! echo "$S" | jq -e "[.task.steps[] | select(.kind==\"launch\" and .target!=\"$CLOCK\")] | length > 0" >/dev/null || logs_all_scope_denied'
note "steps: $(echo "$S" | jq -c '[.task.steps[] | {kind, app, target, outcome}]')"
adb shell cmd statusbar collapse >/dev/null 2>&1

sleep "$COOLDOWN"
echo "== screen reader reminders" | tee -a "$OUT/result.txt"
# As in task_run, a provider problem before the reader goes off is retried
# once after a cooldown.
for attempt in 1 2; do
	fresh_clock
	start_task "screen reader turned off mid-task" "设一个早上 6:30 的闹钟" "6:30"
	wait_line '^step {"task":"[^"]*","seq":2' 120
	T=$(last_task)
	if [ "$attempt" = 1 ] && echo "$T" | grep -Eq 'rate_limited|output_truncated'; then
		note "provider problem ($(echo "$T" | jq -r .code)); retrying once after ${COOLDOWN}s"
		sleep "$COOLDOWN"
		continue
	fi
	break
done
adb shell settings put secure enabled_accessibility_services null
T=$(wait_task 30); note "$T" | cut -c1-200
check "turning the reader off interrupts the task (screen_reader_off)" 'echo "$T" | jq -e ".status==\"interrupted\" and .code==\"screen_reader_off\"" >/dev/null'
check "reminder posted at once" 'logs | grep -q "^reader {\"on\":false,\"reminded\":\"unbound\"}" && notice_titled 1003 "超级牛马的屏幕助手已关闭"'
check "the interrupted task was notified" 'notice_titled 1002 "任务中断"'
adb shell cmd statusbar expand-notifications; sleep 2
shot reader-reminder
# The reader is off, so uiautomator (which suspends accessibility services)
# disturbs nothing here: use it to find the reminder and touch it.
adb shell uiautomator dump /sdcard/shade.xml >/dev/null 2>&1
AT=$(adb shell cat /sdcard/shade.xml | tr '>' '\n' | grep -F 'text="超级牛马的屏幕助手已关闭"' | sed -n 's/.*bounds="\[\([0-9]*\),\([0-9]*\)\]\[\([0-9]*\),\([0-9]*\)\]".*/\1 \2 \3 \4/p' | head -1 | awk '{print int(($1+$3)/2), int(($2+$4)/2)}')
[ -n "$AT" ] && adb shell input tap $AT; sleep 3
check "touching the reminder opens the accessibility settings" 'adb shell dumpsys window | grep mCurrentFocus | grep -q "com.android.settings"'
adb shell input keyevent KEYCODE_HOME
adb logcat -c
adb shell settings put secure enabled_accessibility_services $P/$P.UiTreeService
wait_line "^service connected" 20
check "turning the reader back on withdraws the reminder" '! notice_posted 1003'

adb logcat -c
adb shell am force-stop $P; sleep 2
adb shell am start -n $P/.MainActivity >/dev/null 2>&1; sleep 4
check "a force stop is caught when the app next starts" 'logs | grep -q "^reader {\"on\":false,\"reminded\":\"app_start\"}" && notice_posted 1003'
adb shell input keyevent KEYCODE_HOME
adb logcat -c
adb shell settings put secure enabled_accessibility_services $P/$P.UiTreeService
adb shell settings put secure accessibility_enabled 1
wait_line "^service connected" 20

# Notifications not allowed: nothing is posted, the rest keeps working.
adb logcat -c
adb shell pm revoke $P android.permission.POST_NOTIFICATIONS; sleep 3
adb shell settings put secure enabled_accessibility_services $P/$P.UiTreeService
wait_line "^service connected" 20
adb logcat -c
adb shell settings put secure enabled_accessibility_services null; sleep 3
check "without the notification permission the reminder is skipped, not crashed" 'logs | grep -q "^notice skipped {\"id\":1003" && ! notice_posted 1003'
adb shell pm grant $P android.permission.POST_NOTIFICATIONS
adb logcat -c
adb shell settings put secure enabled_accessibility_services $P/$P.UiTreeService
adb shell settings put secure accessibility_enabled 1
wait_line "^service connected" 20

sleep "$COOLDOWN"
# Every 3rd model request is answered 429 with Retry-After 6 (not processed:
# re-sent in the same reservation) and every 5th 500 (possibly processed:
# re-sent with a new reservation). Neither reaches the provider.
start_relay --inject 429:3:6 --inject 500:5
fresh_clock
RETRY_SHOT=retry-pill
start_task "provider failures are retried" "设一个早上 7:00 的闹钟" "7:00"
for _ in $(seq 1 300); do
	if [ -n "$RETRY_SHOT" ] && logs | grep -q '^retry {.*rate_limited'; then sleep 1.5; shot "$RETRY_SHOT"; RETRY_SHOT=; fi
	last_task | grep -q '"status":"running"' || [ -z "$(last_task)" ] || break
	sleep 1
done
T=$(wait_task 120); note "$T" | cut -c1-200; steps >/dev/null
note "retries: $(logs | grep -c '^retry {'), relay: $(grep -c 'injected' "$OUT/relay.log") injected"
check "task with injected 429s and 500s still done and verified" 'echo "$T" | jq -e ".status==\"done\" and .verified==true" >/dev/null'
check "429 waits were shown with their Retry-After" 'logs | grep -q "^retry {.*\"wait_ms\":6000.*rate_limited"'
check "500s were retried after a backoff" 'logs | grep -q "^retry {.*\"wait_ms\":2000.*execution_http_failed"'
check "injected failures never reached the provider" '[ "$(grep -c "(injected).*-> 429" "$OUT/relay.log")" -ge 1 ] && [ "$(grep -c "(injected).*-> 500" "$OUT/relay.log")" -ge 1 ]'
start_relay

echo "== builds and health" | tee -a "$OUT/result.txt"
check "release build disables the test commands" '"$AAPT2" dump resources "$REL" | grep -A1 "bool/m0_commands" | grep -q false'
check "no crash from the app" '[ "$(adb logcat -d -b crash | grep -c "$P")" = 0 ]'
note "model requests: $(grep -c 'relay POST' "$OUT/relay.log"), statuses: $(grep 'relay POST' "$OUT/relay.log" | awk '{for (i = 1; i <= NF; i++) if ($i == "->") print $(i + 1)}' | sort | uniq -c | tr '\n' ' ')"
echo "== $pass passed, $fail failed" | tee -a "$OUT/result.txt"
[ "$fail" = 0 ]
