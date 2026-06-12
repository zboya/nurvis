package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zboya/nurvis/internal/provider"
	"github.com/zboya/nurvis/internal/store/repo"
)

// PublishCFPages deploys a local static directory to Cloudflare Pages.
// Credentials are loaded from the site_credentials store (provider = "cloudflare").
type PublishCFPages struct {
	creds *repo.SiteCredentialRepo
}

// NewPublishCFPages creates the tool with a credential repository.
func NewPublishCFPages(creds *repo.SiteCredentialRepo) *PublishCFPages {
	return &PublishCFPages{creds: creds}
}

func (*PublishCFPages) Name() string { return "publish_cloudflare_pages" }

func (*PublishCFPages) Description() string {
	return "Publish a local static site directory to Cloudflare Pages via Direct Upload. " +
		"Returns the public *.pages.dev URL. The site directory must be within the workspace. " +
		"Cloudflare credentials (API token + account ID) must be pre-configured in Settings > Credentials."
}

func (*PublishCFPages) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "publish_cloudflare_pages",
		Description: "Deploy a static site to Cloudflare Pages and get the public URL.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "Path to the static directory (relative to workspace or absolute within workspace).",
				},
				"project_name": map[string]any{
					"type":        "string",
					"description": "Cloudflare Pages project name (lowercase, digits, hyphens). Auto-created if needed. Served at <name>.pages.dev.",
				},
				"branch": map[string]any{
					"type":        "string",
					"description": "Deployment branch (optional). Non-production branch creates a preview deployment.",
				},
				"credential_name": map[string]any{
					"type":        "string",
					"description": "Name of the Cloudflare credential to use (optional, defaults to most recent).",
				},
			},
			"required": []string{"path", "project_name"},
		},
	}
}

// cfCred is the parsed credential JSON.
type cfCred struct {
	APIToken  string `json:"api_token"`
	AccountID string `json:"account_id"`
}

func (t *PublishCFPages) Invoke(ctx context.Context, raw json.RawMessage, scope Scope) (*Result, error) {
	if t.creds == nil {
		return &Result{Content: "credential store not available", IsError: true}, nil
	}

	var args struct {
		Path           string `json:"path"`
		ProjectName    string `json:"project_name"`
		Branch         string `json:"branch"`
		CredentialName string `json:"credential_name"`
	}
	_ = json.Unmarshal(raw, &args)

	rawPath := strings.TrimSpace(args.Path)
	projectName := strings.TrimSpace(args.ProjectName)
	branch := args.Branch
	credName := args.CredentialName
	if rawPath == "" {
		return &Result{Content: "path is required", IsError: true}, nil
	}
	if projectName == "" {
		return &Result{Content: "project_name is required", IsError: true}, nil
	}

	// Resolve directory within workspace
	wsDir := scope.WorkspaceDir
	if wsDir == "" {
		return &Result{Content: "no workspace available", IsError: true}, nil
	}

	dir := rawPath
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(wsDir, dir)
	}
	dir = filepath.Clean(dir)

	// Boundary check
	wsClean := filepath.Clean(wsDir)
	if !strings.HasPrefix(dir, wsClean+string(os.PathSeparator)) && dir != wsClean {
		return &Result{Content: "path must be inside the workspace", IsError: true}, nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		return &Result{Content: fmt.Sprintf("cannot access path: %v", err), IsError: true}, nil
	}
	if !info.IsDir() {
		return &Result{Content: "path must be a directory", IsError: true}, nil
	}

	// Load credential
	cred, err := t.loadCredential(ctx, credName)
	if err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}

	slog.Info("cf_pages: starting deploy", "dir", dir, "project", projectName, "branch", branch)

	// Ensure project exists via API (create if 404)
	if err := t.ensureProject(ctx, cred, projectName, branch); err != nil {
		return &Result{Content: err.Error(), IsError: true}, nil
	}

	// Direct Upload flow
	uploader := newCFUploader(cred.APIToken, cred.AccountID, projectName)

	assets, err := scanDir(dir)
	if err != nil {
		return &Result{Content: fmt.Sprintf("scan directory: %v", err), IsError: true}, nil
	}
	slog.Info("cf_pages: assets scanned", "count", len(assets))

	jwt, err := uploader.getUploadJWT(ctx)
	if err != nil {
		return &Result{Content: fmt.Sprintf("get upload token: %v", err), IsError: true}, nil
	}

	uploaded, err := uploader.uploadAssets(ctx, jwt, assets)
	if err != nil {
		return &Result{Content: fmt.Sprintf("upload assets: %v", err), IsError: true}, nil
	}

	deployURL, err := uploader.deploy(ctx, assets, branch)
	if err != nil {
		return &Result{Content: fmt.Sprintf("create deployment: %v", err), IsError: true}, nil
	}

	slog.Info("cf_pages: deployed", "url", deployURL, "files", len(assets), "uploaded", uploaded)

	var sb strings.Builder
	sb.WriteString("Published to Cloudflare Pages.\n")
	fmt.Fprintf(&sb, "- URL: %s\n", deployURL)
	fmt.Fprintf(&sb, "- Project: %s\n", projectName)
	fmt.Fprintf(&sb, "- Files: %d total, %d uploaded (rest cached)\n", len(assets), uploaded)
	if branch != "" {
		fmt.Fprintf(&sb, "- Branch: %s\n", branch)
	}
	return &Result{Content: sb.String()}, nil
}

func (t *PublishCFPages) loadCredential(ctx context.Context, name string) (*cfCred, error) {
	record, err := t.creds.LookupByProvider(ctx, "cloudflare", name)
	if err != nil {
		return nil, fmt.Errorf("lookup credential: %v", err)
	}
	if record == nil {
		if name != "" {
			return nil, fmt.Errorf("no enabled Cloudflare credential named %q found; configure one in Settings > Credentials", name)
		}
		return nil, fmt.Errorf("no Cloudflare credential configured; add one in Settings > Credentials")
	}

	var cred cfCred
	if err := json.Unmarshal([]byte(record.ConfigJSON), &cred); err != nil {
		return nil, fmt.Errorf("credential JSON is invalid")
	}
	if strings.TrimSpace(cred.APIToken) == "" {
		return nil, fmt.Errorf("credential is missing 'api_token'")
	}
	if strings.TrimSpace(cred.AccountID) == "" {
		return nil, fmt.Errorf("credential is missing 'account_id'")
	}
	return &cred, nil
}

// ensureProject checks if the Pages project exists, creating it if not.
func (t *PublishCFPages) ensureProject(ctx context.Context, cred *cfCred, name, branch string) error {
	// Try to get the project; if 404, create it.
	uploader := newCFUploader(cred.APIToken, cred.AccountID, name)

	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s", cfAPIBase, cred.AccountID, name)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+cred.APIToken)

	var env cfEnvelope
	if err := uploader.doRequest(req, &env); err != nil {
		// Project not found — create it
		prodBranch := branch
		if prodBranch == "" {
			prodBranch = "main"
		}
		return t.createProject(ctx, cred, name, prodBranch)
	}
	return nil
}

func (t *PublishCFPages) createProject(ctx context.Context, cred *cfCred, name, prodBranch string) error {
	body, _ := json.Marshal(map[string]any{
		"name":              name,
		"production_branch": prodBranch,
	})
	url := fmt.Sprintf("%s/accounts/%s/pages/projects", cfAPIBase, cred.AccountID)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+cred.APIToken)
	req.Header.Set("Content-Type", "application/json")

	client := uploader_http_client()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("create Pages project: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("create Pages project %q: HTTP %d", name, resp.StatusCode)
	}
	slog.Info("cf_pages: project created", "name", name)
	return nil
}

func uploader_http_client() *http.Client {
	return &http.Client{Timeout: cfRequestTimeout}
}
