# MySQL FULLTEXT ngram 前提（中文知识库检索）

租户知识库检索（`tenant_knowledge_chunks`）用 `MATCH(content) AGAINST (... IN NATURAL LANGUAGE MODE)` 做排序。
MySQL **默认的全文分词器只按空格和标点切词**，中文没有空格，所以：

- 默认 `FULLTEXT` 索引对中文内容**不产生任何 token**；
- 中文 query 的 `MATCH` 恒为 0；
- 检索静默退化成 LIKE 兜底 —— 用户以为在用检索，实际在用子串匹配。

migration `000009_knowledge_fulltext_ngram` 把该索引换成 `WITH PARSER ngram`。
**但 ngram 分词宽度是服务器级参数，migration 改不了，必须由 DBA 在建索引之前配好。**

## 上线前必须做的事

1. 在 `my.cnf` 里显式声明分词宽度（默认 2，即二字组；`0` 等价于关闭中文分词）：

   ```ini
   [mysqld]
   ngram_token_size = 2
   ```

2. **重启 MySQL**。`ngram_token_size` 是只读变量，运行时 `SET` 不生效：

   ```sql
   SHOW VARIABLES LIKE 'ngram_token_size';
   ```

3. 再执行 migration：

   ```bash
   go run ./cmd/niuma tenant migrate up
   ```

   顺序反了（先建索引再改 `ngram_token_size`）会让索引里的 token 宽度和查询时的宽度不一致，必须 `DROP` 后重建索引。

## 已知限制

- **短于 `ngram_token_size` 的 query 命不中**。`ngram_token_size = 2` 时，单字 query（"检"）产生不了 token，只能靠 LIKE 兜底。
- **`ngram_token_size` 越大，索引越小、召回越差**；越小则索引膨胀。2 是中文的常规取值。
- LIKE 兜底是 best-effort：检索串会被切成有界的词项（`internal/storage/mysql/knowledge_search.go`，最多 8 个），中文按标点/空格切分后的短语才能命中。**中文召回质量取决于 ngram FULLTEXT，不要依赖 LIKE 兜底。**
- 本仓**没有 embedding / 向量检索**（`embedding_ref` 是从未写入的 schema 占位符），这是 TODO-025 的既定取舍。

## 如何验证真的走了 FULLTEXT

`SearchKnowledgeChunks` 返回的每一行都带 `search_mode`：`fulltext` 表示 `MATCH` 命中，`like` 表示退化到了兜底。
opt-in e2e 会对中文 query 断言 `search_mode == "fulltext"`：

```bash
NIUMA_MYSQL_E2E_DSN='user:pass@tcp(127.0.0.1:3306)/db?multiStatements=true&parseTime=true&charset=utf8mb4' \
  go test ./internal/tenant -run TestMySQLE2EKnowledgeChineseSearchUsesFulltext -count=1 -v
```

或直接跑 `scripts/tenant-mysql-e2e.sh`（会连带跑这条）。
