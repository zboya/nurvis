-- Site credentials (for deploying to platforms like Cloudflare Pages)
CREATE TABLE IF NOT EXISTS site_credentials (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    provider    TEXT NOT NULL,          -- cloudflare | netlify | vercel ...
    config_json TEXT NOT NULL,          -- encrypted/plaintext credential details JSON
    enabled     INTEGER DEFAULT 1,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_site_credentials_provider_name ON site_credentials(provider, name);
