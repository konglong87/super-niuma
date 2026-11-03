SET NAMES utf8mb4;

CREATE TABLE IF NOT EXISTS media_assets (
  asset_id VARCHAR(128) NOT NULL,
  tenant_id BIGINT UNSIGNED NOT NULL,
  user_id BIGINT UNSIGNED NOT NULL,
  session_id BIGINT UNSIGNED NULL,
  kind VARCHAR(32) NOT NULL,
  media_type VARCHAR(128) NOT NULL,
  name VARCHAR(255) NULL,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  sha256 CHAR(64) NULL,
  state VARCHAR(32) NOT NULL,
  original_json JSON NULL,
  derivatives_json JSON NULL,
  access_json JSON NOT NULL,
  error_message VARCHAR(1024) NULL,
  expires_at DATETIME(6) NULL,
  created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (asset_id),
  UNIQUE KEY uk_media_assets_tenant_hash (tenant_id, sha256),
  KEY idx_media_assets_owner (tenant_id, user_id, session_id),
  KEY idx_media_assets_expiry (expires_at),
  KEY idx_media_assets_state (tenant_id, state),
  CONSTRAINT fk_media_assets_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id),
  CONSTRAINT fk_media_assets_user FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_users(tenant_id, id),
  CONSTRAINT fk_media_assets_session FOREIGN KEY (tenant_id, session_id) REFERENCES tenant_sessions(tenant_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
