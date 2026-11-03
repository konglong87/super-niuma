DROP TABLE IF EXISTS channel_reactions;

ALTER TABLE channel_conversations
  DROP COLUMN permission_mode;
