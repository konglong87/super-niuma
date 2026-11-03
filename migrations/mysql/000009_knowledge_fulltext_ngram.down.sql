SET NAMES utf8mb4;

ALTER TABLE tenant_knowledge_chunks DROP INDEX ft_knowledge_chunks_content_ngram;
ALTER TABLE tenant_knowledge_chunks ADD FULLTEXT KEY ft_knowledge_chunks_content (content);
