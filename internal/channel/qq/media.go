// Package qq's media.go implements QQ official bot rich media (image/video/voice/file) sending.
//
// Protocol: QQ Guild v2 Rich Media Message
//
//	https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/send-receive/rich-media.html
//
// The flow is two steps:
//  1. Upload: POST /v2/users/{openid}/files or /v2/groups/{group_openid}/files
//     Pass in url (public link) or file_data (base64 binary), receive file_info;
//  2. Send: POST /v2/users/{openid}/messages or /v2/groups/{group_openid}/messages
//     Include file_info + optional text content.
//
// This implementation is ported from LongClaw's implementation, with the following adaptations:
//   - Logging switched to log/slog
//   - Client type and construction privatized (not needed by external packages within nurvis)
//   - Added Group upload / send functions in addition to C2C (LongClaw only implemented C2C)
package qq

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/openapi"
	"golang.org/x/oauth2"
)

const (
	apiBase     = "https://api.sgroup.qq.com"
	apiBaseSbox = "https://sandbox.api.sgroup.qq.com"

	defaultAPITimeout = 30 * time.Second
	fileUploadTimeout = 120 * time.Second

	uploadMaxRetries  = 2
	uploadBaseDelayMs = 1000 // 1s
)

// mediaFileType corresponds to QQ Bot v2 rich media file_type values.
type mediaFileType int

const (
	mediaImage mediaFileType = 1
	mediaVideo mediaFileType = 2
	mediaVoice mediaFileType = 3
	mediaFile  mediaFileType = 4
)

// uploadMediaResp is the response from the upload interface.
type uploadMediaResp struct {
	FileUUID string `json:"file_uuid"`
	FileInfo string `json:"file_info"`
	TTL      int    `json:"ttl"`
	ID       string `json:"id,omitempty"`
}

// messageResp is the response from the send interface.
type messageResp struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

// mediaClient encapsulates QQ Bot v2 rich media related HTTP calls.
type mediaClient struct {
	httpClient  *http.Client
	api         openapi.OpenAPI
	tokenSource oauth2.TokenSource
	apiBase     string

	// file_info cache: same content (hash) + same peer + same type can be reused within TTL, saving one upload.
	fileCache sync.Map // key: string, value: *fileCacheEntry
}

type fileCacheEntry struct {
	fileInfo  string
	expiresAt time.Time
}

func newMediaClient(api openapi.OpenAPI, ts oauth2.TokenSource, sandbox bool) *mediaClient {
	base := apiBase
	if sandbox {
		base = apiBaseSbox
	}
	return &mediaClient{
		httpClient:  &http.Client{},
		api:         api,
		tokenSource: ts,
		apiBase:     base,
	}
}

func (c *mediaClient) accessToken() (string, error) {
	t, err := c.tokenSource.Token()
	if err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// apiRequest sends an authenticated HTTP request.
func (c *mediaClient) apiRequest(ctx context.Context, accessToken, method, path string, body any) ([]byte, error) {
	url := c.apiBase + path

	timeout := defaultAPITimeout
	if strings.Contains(path, "/files") {
		timeout = fileUploadTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("create request [%s]: %w", path, err)
	}
	req.Header.Set("Authorization", "QQBot "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request timeout [%s]: exceeded %v", path, timeout)
		}
		return nil, fmt.Errorf("network error [%s]: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response [%s]: %w", path, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		}
		_ = json.Unmarshal(respBody, &apiErr)
		if apiErr.Message != "" {
			return nil, fmt.Errorf("api error [%s]: %s (code=%d)", path, apiErr.Message, apiErr.Code)
		}
		return nil, fmt.Errorf("api error [%s]: %s", path, string(respBody))
	}
	return respBody, nil
}

// apiRequestWithRetry is only used for /files upload, doing exponential backoff retries.
func (c *mediaClient) apiRequestWithRetry(ctx context.Context, accessToken, method, path string, body any) ([]byte, error) {
	var lastErr error

	for attempt := 0; attempt <= uploadMaxRetries; attempt++ {
		respBody, err := c.apiRequest(ctx, accessToken, method, path, body)
		if err == nil {
			return respBody, nil
		}
		lastErr = err
		errMsg := err.Error()

		// Fail immediately on non-retryable errors
		if strings.Contains(errMsg, "400") || strings.Contains(errMsg, "401") ||
			strings.Contains(errMsg, "Invalid") || strings.Contains(errMsg, "timeout") ||
			strings.Contains(errMsg, "Timeout") {
			return nil, lastErr
		}

		if attempt < uploadMaxRetries {
			delay := time.Duration(uploadBaseDelayMs*intPow(2, attempt)) * time.Millisecond
			slog.Warn("qq: upload attempt failed, retrying",
				"attempt", attempt+1, "delay", delay.String(), "err", truncate(errMsg, 120))
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	return nil, lastErr
}

// ── file_info cache ──────────────────────────────────────────

func computeFileHash(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func (c *mediaClient) cachedFileInfo(contentHash, scope, targetID string, ft mediaFileType) (string, bool) {
	key := fmt.Sprintf("%s:%s:%s:%d", contentHash, scope, targetID, ft)
	val, ok := c.fileCache.Load(key)
	if !ok {
		return "", false
	}
	entry := val.(*fileCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.fileCache.Delete(key)
		return "", false
	}
	return entry.fileInfo, true
}

func (c *mediaClient) setCachedFileInfo(contentHash, scope, targetID string, ft mediaFileType, fileInfo string, ttl int) {
	key := fmt.Sprintf("%s:%s:%s:%d", contentHash, scope, targetID, ft)
	c.fileCache.Store(key, &fileCacheEntry{
		fileInfo:  fileInfo,
		expiresAt: time.Now().Add(time.Duration(ttl) * time.Second),
	})
}

// ── C2C (private chat) ─────────────────────────────────────────────

func (c *mediaClient) uploadC2CMedia(
	ctx context.Context, accessToken, openid string,
	ft mediaFileType, url, fileData string,
) (*uploadMediaResp, error) {
	if url == "" && fileData == "" {
		return nil, fmt.Errorf("uploadC2CMedia: url or fileData is required")
	}
	if fileData != "" {
		if cached, ok := c.cachedFileInfo(computeFileHash(fileData), "c2c", openid, ft); ok {
			slog.Info("qq: uploadC2CMedia using cached file_info", "openid", openid)
			return &uploadMediaResp{FileInfo: cached}, nil
		}
	}

	body := map[string]any{
		"file_type":    int(ft),
		"srv_send_msg": false,
	}
	if url != "" {
		body["url"] = url
	} else {
		body["file_data"] = fileData
	}

	path := fmt.Sprintf("/v2/users/%s/files", openid)
	respBody, err := c.apiRequestWithRetry(ctx, accessToken, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("uploadC2CMedia: %w", err)
	}
	var result uploadMediaResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("uploadC2CMedia parse response: %w", err)
	}
	if fileData != "" && result.FileInfo != "" && result.TTL > 0 {
		c.setCachedFileInfo(computeFileHash(fileData), "c2c", openid, ft, result.FileInfo, result.TTL)
	}
	return &result, nil
}

func (c *mediaClient) postC2CMediaMessage(
	ctx context.Context, accessToken, openid, fileInfo, content string,
) (*messageResp, error) {
	body := map[string]any{
		"msg_type": dto.RichMediaMsg,
		"media":    map[string]string{"file_info": fileInfo},
	}
	if content != "" {
		body["content"] = content
	}
	path := fmt.Sprintf("/v2/users/%s/messages", openid)
	respBody, err := c.apiRequest(ctx, accessToken, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("postC2CMediaMessage: %w", err)
	}
	var result messageResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("postC2CMediaMessage parse response: %w", err)
	}
	return &result, nil
}

// SendC2CMedia uploads and sends a rich media to a private chat.
// fileData/url: choose one. fileData is raw bytes, url is a public downloadable link.
func (c *mediaClient) SendC2CMedia(
	ctx context.Context, openid, content string,
	ft mediaFileType, fileData []byte, url string,
) (*messageResp, error) {
	at, err := c.accessToken()
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	var b64 string
	if len(fileData) > 0 {
		b64 = base64.StdEncoding.EncodeToString(fileData)
	}
	up, err := c.uploadC2CMedia(ctx, at, openid, ft, url, b64)
	if err != nil {
		return nil, err
	}
	return c.postC2CMediaMessage(ctx, at, openid, up.FileInfo, content)
}

// ── Group messages ──────────────────────────────────────────────────

func (c *mediaClient) uploadGroupMedia(
	ctx context.Context, accessToken, groupOpenID string,
	ft mediaFileType, url, fileData string,
) (*uploadMediaResp, error) {
	if url == "" && fileData == "" {
		return nil, fmt.Errorf("uploadGroupMedia: url or fileData is required")
	}
	if fileData != "" {
		if cached, ok := c.cachedFileInfo(computeFileHash(fileData), "group", groupOpenID, ft); ok {
			slog.Info("qq: uploadGroupMedia using cached file_info", "group", groupOpenID)
			return &uploadMediaResp{FileInfo: cached}, nil
		}
	}

	body := map[string]any{
		"file_type":    int(ft),
		"srv_send_msg": false,
	}
	if url != "" {
		body["url"] = url
	} else {
		body["file_data"] = fileData
	}

	path := fmt.Sprintf("/v2/groups/%s/files", groupOpenID)
	respBody, err := c.apiRequestWithRetry(ctx, accessToken, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("uploadGroupMedia: %w", err)
	}
	var result uploadMediaResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("uploadGroupMedia parse response: %w", err)
	}
	if fileData != "" && result.FileInfo != "" && result.TTL > 0 {
		c.setCachedFileInfo(computeFileHash(fileData), "group", groupOpenID, ft, result.FileInfo, result.TTL)
	}
	return &result, nil
}

func (c *mediaClient) postGroupMediaMessage(
	ctx context.Context, accessToken, groupOpenID, fileInfo, content string,
) (*messageResp, error) {
	body := map[string]any{
		"msg_type": dto.RichMediaMsg,
		"media":    map[string]string{"file_info": fileInfo},
	}
	if content != "" {
		body["content"] = content
	}
	path := fmt.Sprintf("/v2/groups/%s/messages", groupOpenID)
	respBody, err := c.apiRequest(ctx, accessToken, http.MethodPost, path, body)
	if err != nil {
		return nil, fmt.Errorf("postGroupMediaMessage: %w", err)
	}
	var result messageResp
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("postGroupMediaMessage parse response: %w", err)
	}
	return &result, nil
}

// SendGroupMedia uploads and sends a rich media to a group.
// fileData/url: choose one. Note: QQ groups currently do not support rich media type file (only image/video/voice).
func (c *mediaClient) SendGroupMedia(
	ctx context.Context, groupOpenID, content string,
	ft mediaFileType, fileData []byte, url string,
) (*messageResp, error) {
	at, err := c.accessToken()
	if err != nil {
		return nil, fmt.Errorf("get access token: %w", err)
	}
	var b64 string
	if len(fileData) > 0 {
		b64 = base64.StdEncoding.EncodeToString(fileData)
	}
	up, err := c.uploadGroupMedia(ctx, at, groupOpenID, ft, url, b64)
	if err != nil {
		return nil, err
	}
	return c.postGroupMediaMessage(ctx, at, groupOpenID, up.FileInfo, content)
}

// ── helpers ────────────────────────────────────────────────

func intPow(base, exp int) int {
	r := 1
	for i := 0; i < exp; i++ {
		r *= base
	}
	return r
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// kindToFileType maps channel.Artifact.Kind to QQ Bot's file_type.
// If left blank, it will fall back to simple MIME/suffix recognition, defaulting to file (private chat) or image (group) if unrecognized.
func kindToFileType(kind, mime string) mediaFileType {
	switch strings.ToLower(kind) {
	case "image":
		return mediaImage
	case "video":
		return mediaVideo
	case "audio", "voice":
		return mediaVoice
	case "file":
		return mediaFile
	}
	// MIME fallback
	switch {
	case strings.HasPrefix(mime, "image/"):
		return mediaImage
	case strings.HasPrefix(mime, "video/"):
		return mediaVideo
	case strings.HasPrefix(mime, "audio/"):
		return mediaVoice
	}
	return mediaFile
}