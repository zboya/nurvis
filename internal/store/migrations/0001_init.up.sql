PRAGMA journal_mode = WAL;

-- Schema version
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at INTEGER NOT NULL
);

-- Global KV settings
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT PRIMARY KEY,
    value_json TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Projects / workspaces
CREATE TABLE IF NOT EXISTS projects (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    dir         TEXT NOT NULL,
    description TEXT,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Provider
CREATE TABLE IF NOT EXISTS providers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,       -- yzma | openai_compatible
    base_url    TEXT,                -- yzma local: empty; openai_compatible: endpoint
    config_json TEXT,
    created_at  INTEGER NOT NULL
);

-- Agent
CREATE TABLE IF NOT EXISTS agents (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    role            TEXT,
    system_prompt   TEXT,
    provider_id     TEXT,
    model           TEXT NOT NULL,
    default_project TEXT,
    options_json    TEXT,
    max_rounds      INTEGER DEFAULT 16,
    enabled         INTEGER DEFAULT 1,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);

-- Agent tool whitelist
CREATE TABLE IF NOT EXISTS agent_tools (
    agent_id  TEXT NOT NULL,
    tool_ref  TEXT NOT NULL,        -- builtin:exec | mcp:<server>:<tool> | skill:<id>
    enabled   INTEGER DEFAULT 1,
    PRIMARY KEY (agent_id, tool_ref)
);

-- Builtin tool switches
CREATE TABLE IF NOT EXISTS builtin_tools (
    name        TEXT PRIMARY KEY,
    enabled     INTEGER DEFAULT 1,
    config_json TEXT
);

-- MCP servers
CREATE TABLE IF NOT EXISTS mcp_servers (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    transport   TEXT NOT NULL,      -- stdio | sse | http
    command     TEXT,
    args_json   TEXT,
    url         TEXT,
    env_json    TEXT,
    enabled     INTEGER DEFAULT 1,
    created_at  INTEGER NOT NULL
);

-- MCP grants
CREATE TABLE IF NOT EXISTS mcp_grants (
    server_id TEXT NOT NULL,
    agent_id  TEXT NOT NULL,
    PRIMARY KEY (server_id, agent_id)
);

-- Skill
CREATE TABLE IF NOT EXISTS skills (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    version       TEXT,
    path          TEXT NOT NULL,
    manifest_json TEXT,
    enabled       INTEGER DEFAULT 1,
    created_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS skill_grants (
    skill_id TEXT NOT NULL,
    agent_id TEXT NOT NULL,
    PRIMARY KEY (skill_id, agent_id)
);

-- Sessions
CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    agent_id   TEXT NOT NULL,
    project_id TEXT,
    label      TEXT,
    channel    TEXT,                -- desktop | wechat | qq | cron
    summary    TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

-- Message history
CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    role            TEXT NOT NULL,  -- system|user|assistant|tool
    content         TEXT,
    tool_calls_json TEXT,
    tool_name       TEXT,
    media_json      TEXT,
    tokens          INTEGER,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at);

-- Long-term memory
CREATE TABLE IF NOT EXISTS memories (
    id         TEXT PRIMARY KEY,
    agent_id   TEXT,
    scope      TEXT NOT NULL,       -- global | agent | session
    session_id TEXT,
    kind       TEXT,                -- preference | fact | feedback
    content    TEXT NOT NULL,
    embedding  BLOB,
    created_at INTEGER NOT NULL
);

-- Channel instances
CREATE TABLE IF NOT EXISTS channels (
    id          TEXT PRIMARY KEY,
    type        TEXT NOT NULL,      -- wechat | qq
    name        TEXT NOT NULL,
    config_json TEXT,
    agent_id    TEXT,
    enabled     INTEGER DEFAULT 1,
    created_at  INTEGER NOT NULL
);

-- Channel routes
CREATE TABLE IF NOT EXISTS channel_routes (
    id         TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL,
    peer       TEXT NOT NULL,
    agent_id   TEXT,
    session_id TEXT,
    UNIQUE(channel_id, peer)
);

-- Cron jobs
CREATE TABLE IF NOT EXISTS cron_jobs (
    id                 TEXT PRIMARY KEY,
    name               TEXT NOT NULL,
    spec               TEXT NOT NULL,
    agent_id           TEXT NOT NULL,
    project_id         TEXT,
    prompt             TEXT NOT NULL,
    enabled            INTEGER DEFAULT 1,
    created_at         INTEGER NOT NULL,
    target_channel_id  TEXT,
    target_peer_id     TEXT,
    target_peer_type   TEXT             -- user | group
);

-- Cron job run records
CREATE TABLE IF NOT EXISTS cron_runs (
    id          TEXT PRIMARY KEY,
    job_id      TEXT NOT NULL,
    session_id  TEXT,
    status      TEXT,               -- running | ok | failed
    error       TEXT,
    started_at  INTEGER NOT NULL,
    finished_at INTEGER
);
