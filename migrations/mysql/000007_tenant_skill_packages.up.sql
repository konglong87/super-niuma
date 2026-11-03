ALTER TABLE tenant_skills
  ADD COLUMN package_ref VARCHAR(1024) NULL AFTER config_json,
  ADD COLUMN package_sha256 CHAR(64) NULL AFTER package_ref,
  ADD COLUMN manifest_json JSON NULL AFTER package_sha256,
  ADD COLUMN runtime_ref VARCHAR(1024) NULL AFTER manifest_json,
  ADD KEY idx_tenant_skills_package_sha (tenant_id, package_sha256);
