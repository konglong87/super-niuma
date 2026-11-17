# Super Niuma 手机端（Android，M0）

手机本身就是 Agent：Go 核心在手机上调用模型、读界面，沿用 Super 的授权、预算和独立验收。当前是 **M0 可行性验证**，只证明这条路走得通：Go 核心能在 Android 上运行、能读写 SQLite、能走 HTTPS、能经 Super 执行适配器发起真实模型调用，无障碍服务能快速读出界面元素。**M0 不点击、不输入、不发送任何东西。**

结果与后续计划见 [docs/plans/2026-11-17-mobile-agent.md](../docs/plans/2026-11-17-mobile-agent.md)，原始测量和截图在 [android/build/validation/20261117/m0/](android/build/validation/20261117/m0/)。

## 目录

| 路径 | 内容 |
| --- | --- |
| `go.mod` | 独立的 Go 模块：固定 gomobile 版本，不改动桌面端和服务端依赖；通过 `replace` 编译仓库里的 `internal/...` |
| `niumacore/` | Go 核心，gomobile 绑定后 Kotlin 侧调用 `Niumacore.init / probe / httpsProbe / modelCall / screenSummary` |
| `build-core.sh` | 用 NDK 把 `niumacore` 编成 `android/app/libs/niumacore.aar`（构建产物，不入库） |
| `android/` | Kotlin 薄壳：`MainActivity`（探测界面）、`UiTreeService`（只读无障碍服务）、`UiSnapshot`（界面快照编码） |
| `scripts/dev-model-relay.py` | 仅限开发机的回环模型中转：手机经 `adb reverse` 调用，模型密钥留在电脑上 |

## 构建

需要 Go 1.26+、JDK 17、Android SDK（`ANDROID_HOME`）和 NDK（`sdkmanager "ndk;30.0.16248370"`）。

```bash
mobile/build-core.sh
```

```bash
cd mobile/android && ./gradlew :app:testDebugUnitTest :app:assembleDebug
```

```bash
cd mobile && go test ./niumacore
```

`mobile/` 是嵌套模块，根目录的 `go test ./...` 不会覆盖它，需要单独运行上面这条。

## 在模拟器上复现 M0

```bash
adb install -r -g mobile/android/app/build/outputs/apk/debug/app-debug.apk
```

```bash
adb shell am start -n io.github.konglong87.superniuma.mobile/.M0Commands --es m0 probe
```

```bash
adb logcat -s NiumaM0
```

- **HTTPS**：`--es m0 https --es url https://<模型服务>/v1/models`，返回任意 HTTP 状态（包括 401）即说明 DNS、TLS 和系统根证书正常。
- **真实模型调用**：先在电脑上启动中转（`RELAY_TEST_TOKEN=... python3 -I mobile/scripts/dev-model-relay.py <provider 名称>`），再 `adb reverse tcp:18080 tcp:18080`，然后 `--es m0 model --es model <模型> --es token <同一个测试令牌>`。令牌只用于这个回环中转，不是模型密钥。
- **读界面**：在系统设置的无障碍里打开 “Super Niuma screen reader (M0)”（或 `adb shell settings put secure enabled_accessibility_services io.github.konglong87.superniuma.mobile/io.github.konglong87.superniuma.mobile.UiTreeService`），`--es m0 measure_on` 后切到其他 App，每次切换约 0.8 秒后记录一行 `tree {...}`。

调试入口（`m0` extra）只走 `.M0Commands` 别名：它只在 debug 构建启用，并要求 `android.permission.DUMP`。这个权限只有 `adb shell` 拥有，普通 App 拿不到；从桌面图标进入的 `MainActivity` 忽略所有 extra。
