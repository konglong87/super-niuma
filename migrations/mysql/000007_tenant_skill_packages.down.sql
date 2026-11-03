ALTER TABLE tenant_skills
  DROP KEY idx_tenant_skills_package_sha,
  DROP COLUMN runtime_ref,
  DROP COLUMN manifest_json,
  DROP COLUMN package_sha256,
  DROP COLUMN package_ref;
