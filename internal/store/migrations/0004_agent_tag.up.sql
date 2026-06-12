-- 0004_agent_tag.up.sql
-- Add a "tag" column to agents that classifies the runtime modality:
--   to-text   (default; runs the standard llama agent loop)
--   to-image  (routes chat.send through the gosd image pipeline)
--   to-video  (routes chat.send through the gosd video pipeline)
-- Existing rows default to 'to-text' to preserve current behavior.
ALTER TABLE agents ADD COLUMN tag TEXT NOT NULL DEFAULT 'to-text';
