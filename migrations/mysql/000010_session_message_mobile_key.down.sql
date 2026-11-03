SET NAMES utf8mb4;

-- 先撤索引再撤列。反过来 MySQL 也会把索引连带删掉，但依赖那个连带行为会让回滚脚本
-- 读起来不像它实际做的事。
--
-- 回滚不丢数据：mobile_message_key 是从 content_json 算出来的生成列，本身不存原始
-- 信息，撤掉它 message_key 仍然在 content_json 里。代价与 up 对称 —— 删 STORED
-- 生成列同样是一次整表重建。
DROP INDEX idx_session_messages_mobile_key ON tenant_session_messages;
ALTER TABLE tenant_session_messages DROP COLUMN mobile_message_key;
