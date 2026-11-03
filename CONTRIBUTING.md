# 贡献指南

感谢你帮助改进牛马 Niuma。项目强调运行时稳定性、安全边界、可测试性和文档同步；请保持变更聚焦，并诚实说明尚未覆盖的能力边界。

参与社区前，请阅读 [行为准则](CODE_OF_CONDUCT.md)。安全漏洞不要公开提交，请遵循 [安全策略](SECURITY.md)。

## 10 分钟首次贡献

不需要先修改 Runtime Core 才能贡献。第一次参与可以从以下任务开始：

- 修正文档、断链、平台安装说明或英文表达；
- 增加一个脱敏的 Provider、MCP 或 Skills 配置示例；
- 补充一个真实使用场景、截图或测试说明；
- 复现并整理一个跨平台安装或 Provider 兼容性问题。

先查看带有 `good first issue`、`help wanted`、`documentation` 或 `provider` 标签的 Issue。
提交前请说明改动范围、验证命令和未覆盖边界。

## 开始之前

1. 阅读 [AGENTS.md](AGENTS.md)，了解项目架构、测试、API、数据库和 Git 约定。
2. 修改 runtime 前阅读[全局运行时拓扑与变更影响堪舆图](docs/architecture/global_runtime_topology.md)，定位受影响的 `RT-*` 节点并评估爆炸半径。
3. 使用与 [CI](.github/workflows/ci.yml) 一致的 Go 版本；WebUI 使用 [`web/.nvmrc`](web/.nvmrc) 指定的 Node.js 版本。
4. 先搜索已有 Issue、文档、测试和项目抽象，避免重复实现或破坏现有契约。

## 提交变更

- 一个 PR 聚焦一个问题，避免夹带无关格式化或重构。
- 优先复用标准库、成熟依赖和项目已有抽象；新增依赖需说明必要性与维护成本。
- 新功能和缺陷修复应补测试。测试范围需要与影响范围匹配，不要只验证单个成功样本。
- 对外 API、配置、数据库 schema、CLI 行为或兼容边界变化时，同步相关文档。
- 不要提交密钥、令牌、Cookie、个人路径、私有域名、真实用户数据或未经授权的第三方代码与素材。

常规改动至少执行：

```bash
go test ./... -count=1
git diff --check
```

修改 runtime 或拓扑 registry 时，还需执行：

```bash
go run ./scripts/runtime-topology-check --base HEAD --working-tree \
  --impact <updated-or-none> \
  --blast-radius <B0_LOCAL-to-B5_SHARED_STATE> \
  --reason "<why>"
```

修改 API 时，按 [AGENTS.md](AGENTS.md) 补齐 handler/service/storage 测试，重新生成 Swagger，并验证真实服务链路：

```bash
go test ./internal/server -count=1
swag init -g cmd/niuma/main.go --parseInternal --parseDependency
```

## Pull Request 要求

请完整填写 PR 模板中的以下字段：

- `Topology impact`：`updated` 或 `none`；
- `Blast radius`：`B0_LOCAL` 至 `B5_SHARED_STATE`；
- `Topology reason`：说明受影响节点与契约，或说明现有拓扑为何仍然准确；
- 验证命令、结果以及未覆盖的边界。

提交信息使用简洁英文，例如 `fix: preserve session isolation`。维护者可能要求拆分变更、补充测试或明确迁移和回滚路径。

## 许可证

除非明确另行说明，你提交并由项目接收的贡献将依据 [Apache License 2.0](LICENSE) 授权，无需附加条款。
