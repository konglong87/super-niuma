# termenv 本地 fork（冻结）

本目录是 [`github.com/muesli/termenv`](https://github.com/muesli/termenv) **v0.16.0** 的本地 fork，
通过仓库根 `go.mod` 的 replace 指令生效：

```
replace github.com/muesli/termenv => ./third_party/termenv
```

## 这是一个冻结的 fork——不要在这里做常规改动

本目录**不跟随上游升级**，也不接受与下面这一处补丁无关的修改。需要动 termenv 行为时，
先确认是否能在 `internal/tui` 侧解决；只有当问题必须在库内部修才考虑扩大补丁，并同步更新本文件。

## 补丁内容：唯一一处

相对上游 v0.16.0，只有 `termenv_unix.go` 一个文件、`Output.backgroundColor()` 一个函数被改动
（其余文件与上游逐字节一致，仅少了上游的 `.github/`、`.gitignore`、`.golangci*.yml`、`examples/`）。

删掉了 `Output.backgroundColor()` 开头的 OSC 11（`termStatusReport(11)`）终端背景色查询，即上游的这一段：

```go
	s, err := o.termStatusReport(11)
	if err == nil {
		c, err := xTermColor(s)
		if err == nil {
			return c
		}
	}
```

函数其余部分不动：保留 `COLORFGBG` 环境变量分支和默认黑色（`ANSIColor(0)`）回退，
并在原位置留了一条注释说明为什么不查询。

**原因：** OSC 11 是一次「写查询序列 + 读终端应答」的同步往返。部分终端会把应答回显进 stdin，
而 Bubble Tea 此时已经接管输入，于是终端的回答被当成用户击键读进 TUI 输入框，表现为启动瞬间
输入框里冒出一串乱码。Go Claude 的 TUI 本来就按深色终端配色，不需要真的探测背景色，因此直接
去掉查询是最小修法。

## 校验补丁是否仍然只有这一处

```bash
UP="$(go env GOMODCACHE)/github.com/muesli/termenv@v0.16.0"
diff -r -q "$UP" third_party/termenv | grep -v '^Only in'
# 预期只输出 termenv_unix.go 一行

diff -u "$UP/termenv_unix.go" third_party/termenv/termenv_unix.go
```

若上游缓存不存在，先 `go mod download github.com/muesli/termenv@v0.16.0`。
