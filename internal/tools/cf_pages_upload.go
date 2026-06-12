package tools

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"lukechampine.com/blake3"
)

// Cloudflare Pages Direct Upload protocol.
//
// Implements the wrangler-private protocol:
//  1. Get upload JWT
//  2. Hash files with blake3(base64(content)+ext)[:32]
//  3. Check missing hashes
//  4. Upload missing assets in buckets
//  5. Create deployment with manifest

const (
	cfAPIBase         = "https://api.cloudflare.com/client/v4"
	cfMaxAssets       = 20000
	cfBucketMaxBytes  = 50 * 1024 * 1024 // 50 MiB per bucket
	cfBucketMaxFiles  = 5000
	cfRequestTimeout  = 120 * time.Second
)

// cfAsset represents a single file to deploy.
type cfAsset struct {
	urlPath     string // e.g. "/index.html"
	localPath   string
	hash        string
	contentType string
	size        int64
}

// cfUploader handles the Direct Upload protocol against Cloudflare Pages.
type cfUploader struct {
	token     string
	accountID string
	project   string
	client    *http.Client
}

func newCFUploader(token, accountID, project string) *cfUploader {
	return &cfUploader{
		token:     token,
		accountID: accountID,
		project:   project,
		client:    &http.Client{Timeout: cfRequestTimeout},
	}
}

// cfEnvelope is the Cloudflare API response wrapper.
type cfEnvelope struct {
	Success bool            `json:"success"`
	Errors  []cfError       `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type cfError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cfEnvelope) toError() error {
	if e.Success {
		return nil
	}
	if len(e.Errors) > 0 {
		msgs := make([]string, len(e.Errors))
		for i, er := range e.Errors {
			msgs[i] = fmt.Sprintf("[%d] %s", er.Code, er.Message)
		}
		return fmt.Errorf("cloudflare: %s", strings.Join(msgs, "; "))
	}
	return fmt.Errorf("cloudflare: request failed")
}

// scanDir walks a directory and builds the asset list with blake3 hashes.
func scanDir(dir string) ([]cfAsset, error) {
	var assets []cfAsset
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return err
		}
		base := filepath.Base(path)
		if base == ".DS_Store" || base == "Thumbs.db" {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		ct := mime.TypeByExtension(filepath.Ext(path))
		if ct == "" {
			ct = "application/octet-stream"
		}
		assets = append(assets, cfAsset{
			urlPath:     "/" + filepath.ToSlash(rel),
			localPath:   path,
			hash:        hashAsset(content, path),
			contentType: ct,
			size:        info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("no deployable files found in directory")
	}
	if len(assets) > cfMaxAssets {
		return nil, fmt.Errorf("too many files (%d > %d)", len(assets), cfMaxAssets)
	}
	return assets, nil
}

// hashAsset implements blake3(base64(content) + ext)[:32] hex.
func hashAsset(content []byte, path string) string {
	b64 := base64.StdEncoding.EncodeToString(content)
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	sum := blake3.Sum256([]byte(b64 + ext))
	return hex.EncodeToString(sum[:])[:32]
}

// getUploadJWT fetches a short-lived JWT for asset upload endpoints.
func (u *cfUploader) getUploadJWT(ctx context.Context) (string, error) {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/upload-token", cfAPIBase, u.accountID, u.project)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+u.token)

	var env cfEnvelope
	if err := u.doRequest(req, &env); err != nil {
		return "", err
	}
	var out struct {
		JWT string `json:"jwt"`
	}
	if err := json.Unmarshal(env.Result, &out); err != nil || out.JWT == "" {
		return "", fmt.Errorf("empty upload JWT from Cloudflare")
	}
	return out.JWT, nil
}

// checkMissing returns hashes that Cloudflare does not yet have.
func (u *cfUploader) checkMissing(ctx context.Context, jwt string, hashes []string) (map[string]bool, error) {
	body, _ := json.Marshal(map[string]any{"hashes": hashes})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfAPIBase+"/pages/assets/check-missing", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	var env cfEnvelope
	if err := u.doRequest(req, &env); err != nil {
		return nil, err
	}
	var missing []string
	_ = json.Unmarshal(env.Result, &missing)
	set := make(map[string]bool, len(missing))
	for _, h := range missing {
		set[h] = true
	}
	return set, nil
}

// uploadBucket uploads one batch of assets.
func (u *cfUploader) uploadBucket(ctx context.Context, jwt string, bucket []cfAsset) error {
	type item struct {
		Key      string            `json:"key"`
		Value    string            `json:"value"`
		Metadata map[string]string `json:"metadata"`
		Base64   bool              `json:"base64"`
	}
	payload := make([]item, 0, len(bucket))
	for _, a := range bucket {
		data, err := os.ReadFile(a.localPath)
		if err != nil {
			return err
		}
		payload = append(payload, item{
			Key:      a.hash,
			Value:    base64.StdEncoding.EncodeToString(data),
			Metadata: map[string]string{"contentType": a.contentType},
			Base64:   true,
		})
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cfAPIBase+"/pages/assets/upload", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	var env cfEnvelope
	return u.doRequest(req, &env)
}

// uploadAssets uploads only new (missing) assets in size-limited buckets.
func (u *cfUploader) uploadAssets(ctx context.Context, jwt string, assets []cfAsset) (int, error) {
	// Deduplicate hashes
	hashes := make([]string, 0, len(assets))
	seen := make(map[string]bool)
	for _, a := range assets {
		if !seen[a.hash] {
			hashes = append(hashes, a.hash)
			seen[a.hash] = true
		}
	}

	missing, err := u.checkMissing(ctx, jwt, hashes)
	if err != nil {
		return 0, err
	}
	slog.Info("cf_pages: check-missing", "unique", len(hashes), "missing", len(missing))

	// Collect unique assets to upload, sorted largest-first
	toUpload := make([]cfAsset, 0)
	uploaded := make(map[string]bool)
	for _, a := range assets {
		if missing[a.hash] && !uploaded[a.hash] {
			toUpload = append(toUpload, a)
			uploaded[a.hash] = true
		}
	}
	sort.Slice(toUpload, func(i, j int) bool { return toUpload[i].size > toUpload[j].size })

	// Upload in buckets
	var bucket []cfAsset
	var bucketSize int64
	flush := func() error {
		if len(bucket) == 0 {
			return nil
		}
		slog.Info("cf_pages: uploading bucket", "files", len(bucket), "bytes", bucketSize)
		if err := u.uploadBucket(ctx, jwt, bucket); err != nil {
			return err
		}
		bucket = nil
		bucketSize = 0
		return nil
	}

	for _, a := range toUpload {
		if len(bucket) >= cfBucketMaxFiles || bucketSize+a.size > int64(cfBucketMaxBytes) {
			if err := flush(); err != nil {
				return 0, err
			}
		}
		bucket = append(bucket, a)
		bucketSize += a.size
	}
	if err := flush(); err != nil {
		return 0, err
	}
	return len(toUpload), nil
}

// deploy creates the deployment with the asset manifest.
func (u *cfUploader) deploy(ctx context.Context, assets []cfAsset, branch string) (string, error) {
	manifest := make(map[string]string, len(assets))
	for _, a := range assets {
		manifest[a.urlPath] = a.hash
	}
	manifestJSON, _ := json.Marshal(manifest)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("manifest", string(manifestJSON))
	if branch != "" {
		_ = mw.WriteField("branch", branch)
	}
	_ = mw.Close()

	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/deployments", cfAPIBase, u.accountID, u.project)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	req.Header.Set("Authorization", "Bearer "+u.token)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	var env cfEnvelope
	if err := u.doRequest(req, &env); err != nil {
		return "", err
	}
	var out struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(env.Result, &out)
	return out.URL, nil
}

// doRequest executes HTTP request and parses Cloudflare envelope.
func (u *cfUploader) doRequest(req *http.Request, env *cfEnvelope) error {
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, env); err != nil {
		return fmt.Errorf("cloudflare: unexpected response (status %d): %.500s", resp.StatusCode, data)
	}
	return env.toError()
}
