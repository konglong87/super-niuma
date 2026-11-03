DROP TABLE IF EXISTS image_completion_outbox;
DROP TABLE IF EXISTS image_generation_attempts;

DROP INDEX idx_image_generations_claim ON image_generations;
DROP INDEX idx_image_generations_origin ON image_generations;

ALTER TABLE image_generations
  DROP COLUMN updated_at,
  DROP COLUMN error_class,
  DROP COLUMN retry_of_generation_id,
  DROP COLUMN provider_request_id,
  DROP COLUMN cancel_requested_at,
  DROP COLUMN started_at,
  DROP COLUMN heartbeat_at,
  DROP COLUMN lease_until,
  DROP COLUMN lease_owner,
  DROP COLUMN next_attempt_at,
  DROP COLUMN max_attempts,
  DROP COLUMN attempts,
  DROP COLUMN tool_use_id,
  DROP COLUMN origin_ref_json,
  DROP COLUMN origin_type,
  DROP COLUMN batch_id;
