# 手机端 Agent：方案与 M0 结果

2026-11-17 · 状态：M0 可行性验证完成（模拟器），M1 未开始

## 定位

手机本身就是 Agent：模型在手机上思考、在手机上操作 App，沿用 Super 的规矩——先授权再执行、有预算、风险步骤要人审批、做完要独立验收。

```
手机 App（Kotlin 薄壳：无障碍服务、悬浮状态与停止、通知、审批界面、Keystore）
        │ gomobile 生成的 Java 接口，进程内调用，JSON 进出
        ▼
Go 核心（mobile/niumacore，复用 internal/super 的 control / eventbus / execution / verification / sqlitestore）
        │ 回调接口：Go 定义、Kotlin 实现，由无障碍服务真正操作手机
        ▼
   其他 App
```

实现完全基于 Android 与 Go 的官方公开能力和本仓库已有代码。

## M0：做了什么

| 目标 | 结果 | 证据 |
| --- | --- | --- |
| Go 核心编成 Android 库并记录体积 | 通过。gomobile + NDK 30，arm64 去符号后 `libgojni.so` 17.0 MB，AAR 6.1 MB，release 未签名 APK 19.1 MB | `measurements.json` → `size` |
| Go 核心在手机上运行 | 通过。`go1.26.0 android/arm64`，SQLite 3.45.1（Super 同款 cgo 驱动）读写往返 13–15 ms，首次 149 ms | `app-core-probes.png` |
| 手机直连 HTTPS | 通过。对模型服务的无凭据请求得到 401，342–661 ms：DNS、TLS、系统根证书正常 | 同上 |
| 经 Super 执行适配器发起真实模型调用 | 通过（修复两处后）。2.0 s / 2.8 s，用量完整记录，每次只发 1 个请求、不重试 | `measurements.json` → `model_call` |
| 一个普通界面的元素 200 ms 内读完 | 通过。23 次读取中 21 次在 200 ms 内；设置首页 66 个元素 25–48 ms；Kotlin→Go 交接通常 < 7 ms。两次慢读（0.74 s）都是 App 首次冷启动 | `measurements.json` → `accessibility_tree` |

冷启动（`am start -W`）：安装后首次 1450 ms，强行停止后 573–697 ms。

测试环境：Android 14 模拟器（`google_apis` arm64，Apple M4 宿主）。多数 Google 应用停在首次使用引导页，元素数偏少；设置是代表性的日常界面。**尚未在真机上测试。**

## M0 发现的问题与处理

1. **App 进程没有 `$HOME`**：`super.StateDirectory()` 取不到默认的 `~/.super-niuma`，执行器按"授权失效"拒绝（正确的失败关闭）。处理：`niumacore.Init(stateDir)` 把 Super 状态目录显式设到 App 私有目录 `files/super-state`，模型工作区是它的兄弟目录。
2. **SELinux 不允许 App 读取 `/`，`/data`、`/data/data` 为 0771**：执行器校验工作根时从 `/` 起用 `O_RDONLY` 逐级打开，在手机上必然失败（`avc: denied { read } ... tclass=dir`）。处理：Linux 上改用 `O_PATH` 逐级打开（只需穿过权限，仍带 `O_NOFOLLOW|O_DIRECTORY`，末端 inode 比对不变），macOS 保持 `O_RDONLY`。见 `internal/super/execution/files_walk_{linux,darwin}.go`。
   - 新增 Linux 测试：不可列目录的父路径能通过、符号链接父路径仍被拒绝。
   - 把 execution 的测试编成 Android 可执行文件、在模拟器内核上以 shell 身份运行：44 项通过；1 项失败是测试夹具本身无法在 `/data/local/tmp` 建硬链接和 FIFO（SELinux 拒绝），不是被测代码问题。
3. **强行停止会关掉无障碍服务**：`am force-stop` 后系统把服务从已启用列表移除（设置值变为 `null`），必须由用户重新开启。M1 要能检测并引导。
4. **gomobile 会升级 golang.org/x 依赖**：把手机核心放进嵌套模块 `mobile/go.mod`，根模块依赖不变。根目录 `go test ./...` 不覆盖 `mobile/`。
5. **gomobile 构建会对整个依赖图执行 `go mod tidy`**：默认 `goproxy.io` 偶发连接重置，构建时可临时指定 `GOPROXY=https://goproxy.cn,https://proxy.golang.org,direct`。

## 后续计划（按优先级）

| 序号 | 项目 | 依赖 | 验收门槛 |
| --- | --- | --- | --- |
| 1 | 手机工具接入 execution：读屏、点击、输入、滑动、按键、打开 App，按 `super_*` 命名；每次调用前 `CheckAuthority` 并检查 App 白名单 | Go 定义的设备回调接口（gomobile 接口回调） | 单测覆盖越权与白名单；模拟器上真实模型完成"设一个 7:00 闹钟" |
| 2 | 运行中逐步审批：风险步骤暂停等人，超时即拒绝；执行前重读界面，目标变化则不执行 | control 新增待审批状态 | 单测 + 模拟器上"发送短信"停下等审批 |
| 3 | 动作风险分级：允许 / 询问 / 拒绝；发送、付款、删除、安装、拨号需询问；密码与验证码输入框拒绝并交还给人 | 1 | 规则单测 |
| 4 | 悬浮状态条与停止、通知、审批界面（只有人能点） | 2 | 模拟器原生截图 |
| 5 | 模型密钥：Keystore 存储，或经 `niuma server` 中转 | — | 密钥不进 APK、不进日志 |
| 6 | 无障碍服务被关闭（如强行停止）时的检测与重新开启引导 | — | 模拟器复现 |
| 7 | CI：`mobile/` 模块的 Go 测试和 Kotlin 单测 | — | CI 通过 |
| 8 | 真机验证（含国内厂商系统的后台限制） | 1–4 | 真机截图与记录 |

明确延后：M2 体验（语音、分享、定时、记忆）、M3 与桌面 Super 联动、M4 iOS（iOS 无公开接口操作其他 App）。分发方式待定：Google Play 对用无障碍服务做自主 Agent 有限制，上架前需核实官方政策。
