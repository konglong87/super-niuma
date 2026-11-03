SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS tenant_knowledge_documents (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  title VARCHAR(255) NOT NULL,
  source_type VARCHAR(64) NOT NULL DEFAULT 'manual',
  content LONGTEXT NOT NULL,
  metadata_json JSON NULL,
  status VARCHAR(32) NOT NULL DEFAULT 'active',
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  KEY idx_knowledge_documents_tenant_status (tenant_id, status, deleted_at, updated_at),
  KEY idx_knowledge_documents_user_recent (tenant_id, user_id, updated_at),
  CONSTRAINT fk_knowledge_documents_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_knowledge_documents_user FOREIGN KEY (user_id) REFERENCES tenant_users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tenant_knowledge_chunks (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  tenant_id BIGINT UNSIGNED NOT NULL,
  document_id BIGINT UNSIGNED NOT NULL,
  chunk_index INT UNSIGNED NOT NULL,
  content TEXT NOT NULL,
  metadata_json JSON NULL,
  embedding_ref VARCHAR(255) NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  deleted_at TIMESTAMP(6) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_knowledge_chunks_doc_index (document_id, chunk_index),
  KEY idx_knowledge_chunks_tenant_doc (tenant_id, document_id, chunk_index, deleted_at),
  FULLTEXT KEY ft_knowledge_chunks_content (content),
  CONSTRAINT fk_knowledge_chunks_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_knowledge_chunks_document FOREIGN KEY (document_id) REFERENCES tenant_knowledge_documents(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
