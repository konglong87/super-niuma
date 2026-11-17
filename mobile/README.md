# Super Niuma 手机端（Android，M1）

手机本身就是 Agent：Go 核心在手机上调用模型、读界面、操作 App，沿用 Super 的规矩——只在你允许的应用里动手、有预算和时限、发送/付款/删除这类风险步骤先等你确认、做完重新读屏验收。

- **M0**：证明 Go 核心能在 Android 上运行、读写 SQLite、走 HTTPS、经 Super 执行适配器调用模型，无障碍服务能快速读界面。
- **M1**：Agent 能真正完成任务（点击、输入、滚动、按键、打开应用），风险步骤在悬浮卡片上等你批准，随时可以停止，任务和每一步都有记录。

方案、结果和剩余事项见 [docs/plans/2026-11-17-mobile-agent.md](../docs/plans/2026-11-17-mobile-agent.md)；验收记录和截图在 [android/build/validation/20261117/](android/build/validation/20261117/)。

## 目录

| 路径 | 内容 |
| --- | --- |
| `go.mod` | 独立的 Go 模块：固定 gomobile 版本，不改动桌面端和服务端依赖；通过 `replace` 编译仓库里的 `internal/...` |
| `phone/` | 手机这一侧的"门卫"：界面模型、应用范围、风险分级、运行中审批、任务运行与记录、读屏验收 |
| `niumacore/` | gomobile 绑定的对外接口：`Init / OpenTasks / SetBridge / SetListener / StartTask / StopTask / Decide / TaskState`，以及 M0 探测 |
| `build-core.sh` | 用 NDK 把 Go 核心编成 `android/app/libs/niumacore.aar`（构建产物，不入库） |
| `android/` | Kotlin 薄壳：`UiTreeService`（无障碍服务，即设备桥）、`Overlay`（状态条与审批卡片）、`SecretStore`（Keystore 加密的模型密钥）、`MainActivity`（任务与设置） |
| `scripts/dev-model-relay.py` | 仅限开发机的回环模型中转：手机经 `adb reverse` 调用，模型密钥留在电脑上 |
| `scripts/m1-acceptance.sh` | 模拟器上的 M1 端到端验收（真实模型、真实点击悬浮按钮） |

## 构建与测试

需要 Go 1.26+、JDK 17、Android SDK（`ANDROID_HOME`）和 NDK（`sdkmanager "ndk;30.0.16248370"`）。

```bash
mobile/build-core.sh
```

```bash
cd mobile/android && ./gradlew :app:testDebugUnitTest :app:assembleDebug :app:assembleRelease
```

```bash
cd mobile && go test -race ./...
```

`mobile/` 是嵌套模块，根目录的 `go test ./...` 不会覆盖它；CI 的 race 作业和 `mobile-android` 作业会单独跑。

## 使用

1. 安装后在系统的无障碍设置里打开“超级牛马 · 屏幕助手”。没开时，主界面会提示，任务也会被拒绝。
2. 在“模型”里填服务地址（必须是 https）、模型名和 API 密钥，点“保存”。密钥用 Android Keystore 中不可导出的密钥加密保存，不进日志、不进任务记录。
3. 在“任务”里写要做什么、允许使用的应用包名（逗号分隔），以及完成后屏幕上应出现的文字（可选，用于验收），点“开始”。
4. 任务进行时，右上角有一个小状态条和“停止”按钮。遇到发送、付款、删除、安装、拨号、发布、转账、提交这类步骤，底部会弹出卡片，选“允许一次”或“拒绝”；60 秒不处理按拒绝算。
5. 结束后，主界面显示状态：完成并已验收 / 需要你检查 / 失败 / 已停止 / 中断（应用曾被关闭，不会自动重试）。

## 在模拟器上验收

```bash
mobile/scripts/m1-acceptance.sh <provider 名称> mobile/android/build/validation/$(date +%Y%m%d)/m1
```

脚本会清掉时钟应用的数据并修改无障碍设置，只在测试用模拟器上跑。它用 `~/.niuma/settings.json` 里的一个对话补全接口作为模型（经本机中转，密钥不进手机），审批和停止都通过真实点击悬浮按钮完成。

调试命令（`m0` extra）只走 `.M0Commands` 别名：它只在 debug 构建启用，并要求 `android.permission.DUMP`。这个权限只有 `adb shell` 拥有，普通 App 拿不到；从桌面图标进入的 `MainActivity` 忽略所有 extra。注意：从 adb 发 `m1_decide` 会把主界面拉到前台、盖住正在操作的应用，被盖住的应用上的步骤会被拒绝执行；验收脚本因此改为点击悬浮卡片。
