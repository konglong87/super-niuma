SET NAMES utf8mb4;

-- message_key 是移动端的幂等键：客户端重试时靠它判断「这条请求是不是已经处理过」。
-- 它埋在 content_json 的 $.mobile.message_key 下，没有独立列，所以 handler 原先只能
-- ListMessages(…, 1000) 拉一页回来线性查找 —— 而 normalizeLimit 把超过 500 的 limit
-- 静默夹到 500，于是长会话里重试**找不到**已有记录：重新跑一次查询、重新写一条消息、
-- 重新计一次量（TODO-118）。这一族夹取 bug 里只有这一处花钱且有副作用。
--
-- 生成列 + 索引，而不是在查询里写 JSON 路径：JSON 路径表达式用不上索引，每次幂等
-- 检查都要扫完整个会话再逐行解 JSON —— 那只是把线性扫描从 Go 搬到 MySQL 里。
--
-- 列型是 LONGTEXT，与 JSON_UNQUOTE 的返回类型一致，不是 VARCHAR(n)：
-- message_key 由客户端提供、长度没有上限，而这张表在**每条消息的写入路径**上。
-- 声明成 VARCHAR(n) 的话，任何超长 key 在 strict 模式下会让 INSERT 直接报错，
-- 存量数据里若已有超长 key，下面这条 ALTER 的回填也会当场失败。LONGTEXT 装得下
-- 任何输入，两种失败都不存在。
-- 代价是 TEXT 类型建索引必须给前缀长度（下面取 191 字符，utf8mb4 下 764 字节，
-- 同时低于 767 和 3072 两个前缀上限）。**相等判断比的仍然是完整列值** ——
-- 前缀只用于缩小候选行，MySQL 会回表核对，所以不存在「前缀相同就误判为重放」。
--
-- 该列在下面三种情况下都是 SQL NULL：content_json 为 NULL、没有 $.mobile、
-- 或 $.mobile 下没有 message_key。非移动端写入的行（例如 tool_calls 元数据）都落在
-- 这里。content_json 是 JSON 列，MySQL 在写入时就拒绝非法 JSON，所以「content_json
-- 里是非法 JSON」这个状态在这张表里不存在，不需要为它约定取值。
--
-- 上线代价（重要）：STORED 生成列在 ALTER TABLE 时会**回填全表**。MySQL 8.0 下
-- 给表加 STORED 生成列不支持 INSTANT 也不支持 INPLACE，只能走 COPY 算法 ——
-- 整表重建 + 期间禁写，停机时间与行数成正比。大表上请按「一次锁表重建」安排：
-- 低峰期执行，或改用 gh-ost / pt-online-schema-change 之类的在线改表工具。
-- 选 STORED 而不是 VIRTUAL 正是为了这个索引：VIRTUAL 列虽然免回填，但每次读都要
-- 重新解一次 JSON，而幂等检查在写入路径上，读放大比一次性的改表代价更贵。
ALTER TABLE tenant_session_messages
  ADD COLUMN mobile_message_key LONGTEXT
  GENERATED ALWAYS AS (JSON_UNQUOTE(JSON_EXTRACT(content_json, '$.mobile.message_key'))) STORED;

-- 列顺序对应查询的 WHERE：先定位会话，再按 role 区分（同一个 message_key 会同时落在
-- 一条 user 和一条 assistant 上，必须能分开取），最后比 key。
-- 与 uk_session_messages_turn 一样不带 tenant_id / user_id —— 那两列在 WHERE 里是
-- 归属校验，不负责选择率，session_id 本身已经足够窄。
CREATE INDEX idx_session_messages_mobile_key
  ON tenant_session_messages (session_id, role, mobile_message_key(191));
