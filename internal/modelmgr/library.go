package modelmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 模型结构体（按需扩展字段）
type Model struct {
	ID            string    `json:"id"`      // 模型 id，如 "Qwen/Qwen2.5-7B"
	ModelID       string    `json:"modelId"` // 同 id
	Likes         int       `json:"likes"`
	Downloads     int       `json:"downloads"`
	TrendingScore float64   `json:"trendingScore"`
	Tags          []string  `json:"tags"`
	PipelineTag   string    `json:"pipeline_tag"`
	LibraryName   string    `json:"library_name"`
	CreatedAt     time.Time `json:"createdAt"`
	LastModified  time.Time `json:"lastModified"`

	// full=true / safetensors 时可能返回，用于参数量过滤
	Safetensors *struct {
		Total int64 `json:"total"` // 参数总量
	} `json:"safetensors,omitempty"`
}

func (m *manager) ListLibrary(ctx context.Context, search string, limit int) ([]Model, error) {
	base := "https://huggingface.co/api/models"

	if limit <= 0 {
		limit = 30
	}

	q := url.Values{}
	q.Set("filter", "gguf")        // library=gguf
	q.Set("sort", "trendingScore") // sort=trending
	q.Set("direction", "-1")       // 降序
	q.Set("full", "true")          // 返回完整信息（含 safetensors 等）
	q.Set("search", search)
	q.Set("limit", fmt.Sprintf("%d", limit))

	reqURL := base + "?" + q.Encode()

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)

	// 可选：带上 token 提升限额（私有/受限模型必须）
	if token := m.resolveHFToken(ctx); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "go-hf-client/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var models []Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, err
	}
	return models, nil
}

// RepoFile 描述 HF repo 内的一个文件（仅保留前端关心的字段）。
type RepoFile struct {
	Path string `json:"path"`           // 文件相对路径
	Size int64  `json:"size,omitempty"` // bytes
	Type string `json:"type,omitempty"` // file | directory
}

// ListRepoFiles lists files inside a HuggingFace model repo on the `main` branch.
// Only files (not directories) are returned. Caller may filter by extension.
func (m *manager) ListRepoFiles(ctx context.Context, repo string) ([]RepoFile, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, fmt.Errorf("modelmgr: empty repo")
	}
	reqURL := fmt.Sprintf("https://huggingface.co/api/models/%s/tree/main?recursive=true", repo)
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	if token := m.resolveHFToken(ctx); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "go-hf-client/1.0")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var files []RepoFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}
	return files, nil
}