SET NAMES utf8mb4;

-- 000003 created a plain FULLTEXT index. MySQL's default full-text parser only
-- splits on whitespace and punctuation, so CJK content produces no tokens and
-- MATCH(content) AGAINST(<chinese query>) is permanently 0 -- knowledge search
-- silently degraded to the unranked LIKE fallback for every Chinese query.
--
-- The ngram parser tokenizes CJK text into fixed-width n-grams. Its width comes
-- from the server-level, read-only ngram_token_size variable (default 2), which
-- must be set in my.cnf before this index is built; queries shorter than
-- ngram_token_size characters still produce no tokens. See
-- docs/deployment/mysql_fulltext_ngram.md.
ALTER TABLE tenant_knowledge_chunks DROP INDEX ft_knowledge_chunks_content;
ALTER TABLE tenant_knowledge_chunks ADD FULLTEXT KEY ft_knowledge_chunks_content_ngram (content) WITH PARSER ngram;
