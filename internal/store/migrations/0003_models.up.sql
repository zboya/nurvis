-- Persisted model pull jobs and model metadata. Survives process restarts
-- so the frontend can restore the pull progress UI, list installed models,
-- and let the user retry interrupted downloads.
--
-- The table doubles as the canonical "installed models" registry: rows with
-- status='success' represent models that are present on disk and ready to
-- use. The fields below combine two sources:
--   1. HuggingFace single-model API (https://huggingface.co/api/models/{repo})
--      → pipeline_tag, tags, modalities (text/image/audio/video).
--   2. Local filesystem after download → local_path, size_bytes (file size).
CREATE TABLE IF NOT EXISTS models (
    model        TEXT PRIMARY KEY,        -- canonical "<repo>/<file>" identifier
    repo         TEXT NOT NULL,
    file         TEXT NOT NULL,
    status       TEXT NOT NULL,           -- queued | resolving | downloading | verifying | success | error | interrupted
    total_bytes  INTEGER NOT NULL DEFAULT 0,
    current_bytes INTEGER NOT NULL DEFAULT 0,
    percent      REAL    NOT NULL DEFAULT 0,
    error        TEXT,
    started_at   INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    finished_at  INTEGER,

    -- HuggingFace metadata (filled at pull start from the model detail API).
    pipeline_tag TEXT,                    -- e.g. "text-generation", "image-text-to-text"
    tags_json    TEXT,                    -- JSON array of HF tags
    modalities_json TEXT,                 -- JSON array: ["text","image","audio","video"]

    -- Local file metadata (filled on success).
    local_path     TEXT,
    size_bytes     INTEGER NOT NULL DEFAULT 0 -- on-disk size after download
);

CREATE INDEX IF NOT EXISTS idx_models_status ON models(status);
