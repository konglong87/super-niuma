# Update Strategy

本文档记录 golang-cc 自动更新 P2 策略。默认目标是“可发现更新，但不破坏开发工作区”。

## 配置项

```yaml
update:
  enabled: true
  checkOnStartup: true
  checkOnly: false
  autoPull: true
  skipWhenDirty: true
  strategy: git-ff-only
  repoDir: ""
  customCommand: ""
  versionSourceURL: ""
  scheduleInterval: daily
  timeoutSeconds: 30
```

- `enabled`：总开关。
- `checkOnStartup`：进程启动时是否检查。
- `checkOnly`：只检查，不执行 `git pull` 或自定义更新命令。
- `autoPull`：允许默认 `git fetch` + `git pull --ff-only --quiet`。
- `skipWhenDirty`：工作区有未提交变更时跳过更新，默认开启。
- `strategy`：当前支持 `git-ff-only` 和 `check-only`。
- `repoDir`：显式指定 golang-cc 源码目录；为空时从当前工作目录或可执行文件目录推断。
- `customCommand`：自定义更新命令。只在 `checkOnly=false` 且 `autoPull=true` 时运行。
- `versionSourceURL`：纯文本 latest version 地址；`checkOnly=true` 时可不依赖 git worktree。
- `scheduleInterval`：启动检查节流间隔，支持 `6h`、`hourly`、`daily`、`weekly`。
- `timeoutSeconds`：检查或更新命令超时。

## 推荐模式

开发机推荐：

```yaml
update:
  enabled: true
  checkOnStartup: true
  checkOnly: true
  autoPull: false
  versionSourceURL: "https://example.com/golang-cc/latest.txt"
  scheduleInterval: daily
```

这样只提示版本差异，不改动本地代码。

受控部署环境推荐：

```yaml
update:
  enabled: true
  checkOnStartup: true
  checkOnly: false
  autoPull: true
  skipWhenDirty: true
  strategy: git-ff-only
  scheduleInterval: daily
```

这只允许 fast-forward 更新，遇到本地修改会跳过。

需要编译安装的环境可以使用：

```yaml
update:
  enabled: true
  checkOnStartup: true
  checkOnly: false
  autoPull: true
  skipWhenDirty: true
  customCommand: "git pull --ff-only --quiet && make install"
  scheduleInterval: weekly
```

## 风险边界

- 自动更新不会在 dirty worktree 上运行，除非显式关闭 `skipWhenDirty`。
- `customCommand` 是 shell 命令，只应放在受控环境和可信配置中。
- `versionSourceURL` 只用于版本发现，不验证签名，也不下载二进制。
- `scheduleInterval` 是本地节流，不是后台 daemon；它避免频繁启动时重复检查。
