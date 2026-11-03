UPDATE tenant_users
SET role = 'member'
WHERE role IS NULL;

ALTER TABLE tenant_users
  MODIFY COLUMN role VARCHAR(64) NOT NULL DEFAULT 'member';
