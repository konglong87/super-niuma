SET NAMES utf8mb4;

ALTER TABLE agent_team_members
  ADD COLUMN execution_override_json JSON NULL AFTER workspace_policy_json;
