# Team 工作台复审：最终构建原生验收

日期：2026-10-30。范围：桌面 V2 Team UI/UX，不改变模型配置、Computer Use、screen profile 或单 Agent 流程。

## 构建、环境与边界

- 源码提交 `64f2991134a36715df6379770217ed48299fb4c7`；用缓存的 Go 1.26 执行 `COMPUTER_USE_RELEASE_CANDIDATE=1 bash scripts/build-desktop-v2.sh`。`.app/Contents/Resources/computer-use-build.json` 显示 source/web 均 clean，release candidate=true；本地 ad-hoc 签名及 `codesign --verify --deep --strict` 通过。构建 ID 与每张截图的 SHA-256、尺寸见本地 `desktop-v2/build/validation/20261030/team-ui-review/evidence-manifest.json`。
- 全程使用隔离 HOME、`NIUMA_DESKTOP_CONFIG_DIR`、`NIUMA_CONFIG_DIR`、工作目录和 SQLite。模型配置仅含 dummy key 与 `127.0.0.1` 离线 OpenAI-compatible fixture，不转发请求、不保存原始提示词/Auth；无真实付费模型调用。实机点击与截图由原生 Computer Use 接口完成，JPEG 以 `sips` 编码为 PNG，不用浏览器 fixture 冒充原生截图。
- 先在上一已提交构建 `dd7af8a` 用实际点击创建/发布 Agent，验证保存后切换模型模式不再保留过期成功提示；三步 Run 完成 3/3，fixture 记录依赖产物数依次为 0/1/1。再注入 6 次本机 HTTP 503，首步经两次 Agent 尝试仍失败，下游两步均为 blocked、零产物。发现深色窗口 `<select>` 呈灰色渐变条、值难辨，故做局部主题样式修复 `64f2991`；前一构建的截图已清理，不作为最终构建证据。

## 最终构建真实桌面验收

五张 **1024×700** PNG 均来自源码 `64f2991` 的干净树签名 `.app`，位于 `desktop-v2/build/validation/20261030/team-ui-review/`，由 Git 忽略但保留在本机供复核。

| 操作 | 原生可见结果 | 截图 |
|---|---|---|
| Agent 列表与编辑器 | 两个目录操作按钮同排不换行，深色角色/模型下拉框、标签与当前值可读 | `final-01-agent-controls-1024x700.png` |
| 选择角色并保存 | 从原生下拉菜单选“审查者”，新草稿列表持久显示该角色 | `final-02-role-persisted-1024x700.png` |
| 重启后点击旧成功 Run | 从隔离 SQLite 读回 3/3 完成、三个结果产物、Token 数值 | `final-03-completed-1024x700.png` |
| 点击旧失败 Run | 0/3 完成，首步两次失败尝试，未知用量另列保守预留，不误报完成或免费 | `final-04-failed-1024x700.png` |
| 折叠失败尝试并滚动 | Researcher 与 Reviewer 均“已阻塞”，无伪造产物，事件仍可展开 | `final-05-blocked-1024x700.png` |

成功和失败 Run 在旧提交中通过真实原生按钮及离线假模型创建；最终提交重新构建后，**在最终 `.app` 内真实点击历史项并从同一隔离 SQLite 读回、拍摄**。没有声称最终构建再次调用模型。最小尺寸通过退出 macOS 全屏后在真实 1024×700 窗口检查；旧 1230×768 截图没有混入最终证据。

## 自动化与清理

- `cd web && npm test -- --run src/v2/workflows`：43/43 通过。
- `GOTOOLCHAIN=local go test ./internal/agentteam/...`（缓存的 Go 1.26）：四个相关包通过。
- `cd web && npm run typecheck`：通过。
- `cd web && ./node_modules/.bin/biome lint e2e/workflows-v2.spec.ts src/v2/workflows/workflows.css`：通过。
- `cd web && npm run test:e2e -- e2e/workflows-v2.spec.ts --project=chromium-webui-v2-desktop --project=chromium-webui-v2-narrow --project=chromium-webui-v2-mobile`：18/18 通过，包含暗色 1024px computed-style、中文下拉选择与响应式无横向溢出。浏览器截图已删除，不作为原生证据。
- Wails macOS 构建、签名校验通过。隔离 app/service/fixture 进程停止，dummy settings、SQLite、日志、工作目录、临时 JPEG 与旧构建截图均清理；未触碰用户原有进程、真实配置或 `go-e2e`。

仍未覆盖真实付费模型、Apple 公证/发行签名、任意 DAG 分支/循环等既定延期项。全量前端 Biome lint 的历史问题不属于本次 CSS/测试改动；本次改动文件的 lint 通过。
