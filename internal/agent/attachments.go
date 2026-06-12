// Package agent — Attachment handling: converts local file paths uploaded by the user
// into model-recognizable content.
//
// Design goals:
//   - The frontend only passes **absolute** file paths; the backend handles reading and decoding.
//   - Images: base64-encoded, attached to the first user message's Images field (multimodal).
//   - Text/code: content read and appended to the task, wrapped in <attached_file> tags.
//   - Binary non-image: rejected with an error (chat.send returns invalid_params).
//
// Size limits: 256KB per text file, 8MB per image. Exceeding either triggers an error
// to avoid bloating the context window.
package agent

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxTextFileSize  = 256 * 1024       // 256KB
	maxImageFileSize = 8 * 1024 * 1024  // 8MB
)

// AttachmentKind distinguishes file categories.
type AttachmentKind int

const (
	AttachmentText AttachmentKind = iota
	AttachmentImage
)

// Attachment holds the result of a loaded file.
type Attachment struct {
	Path   string         // Original absolute path
	Name   string         // Base name
	Kind   AttachmentKind
	Text   string         // File text content when Kind=Text
	Base64 string         // Base64 encoding when Kind=Image
	MIME   string         // Image MIME type (e.g. image/png)
}

// LoadAttachments reads all attachment paths. When requireVision=false, encountering
// an image directly returns an error, used for early validation in chat.send.
func LoadAttachments(paths []string, requireVision bool) ([]Attachment, error) {
	out := make([]Attachment, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("file path must be absolute: %s", p)
		}
		fi, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("not a regular file: %s", p)
		}

		mime := detectMIME(p)
		isImage := strings.HasPrefix(mime, "image/")

		if isImage {
			if !requireVision {
			return nil, fmt.Errorf("current model does not support images; please remove %s or switch to a vision-capable model", filepath.Base(p))
			}
			if fi.Size() > maxImageFileSize {
		return nil, fmt.Errorf("image %s exceeds %dMB limit", filepath.Base(p), maxImageFileSize/1024/1024)
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", p, err)
			}
			out = append(out, Attachment{
				Path:   p,
				Name:   filepath.Base(p),
				Kind:   AttachmentImage,
				Base64: base64.StdEncoding.EncodeToString(data),
				MIME:   mime,
			})
			continue
		}

		// Non-image: must be text-like
		if !isLikelyText(mime, p) {
		return nil, fmt.Errorf("unsupported file type %s (%s); only text and image files are supported", filepath.Base(p), mime)
		}
		if fi.Size() > maxTextFileSize {
		return nil, fmt.Errorf("text file %s exceeds %dKB limit", filepath.Base(p), maxTextFileSize/1024)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		out = append(out, Attachment{
			Path: p,
			Name: filepath.Base(p),
			Kind: AttachmentText,
			Text: string(data),
			MIME: mime,
		})
	}
	return out, nil
}

// detectMIME identifies MIME type by extension first (for source code files),
// falling back to http.DetectContentType.
func detectMIME(p string) string {
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	}
	// Recognize as code/text by extension
	if _, ok := textExt[ext]; ok {
		return "text/plain"
	}
	// Fallback: sniff the first 512 bytes
	f, err := os.Open(p)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	return http.DetectContentType(buf[:n])
}

// isLikelyText determines whether a file can be injected as text into the prompt.
func isLikelyText(mime, p string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	if mime == "application/json" || mime == "application/xml" ||
		mime == "application/javascript" || mime == "application/x-yaml" {
		return true
	}
	if _, ok := textExt[strings.ToLower(filepath.Ext(p))]; ok {
		return true
	}
	return false
}

// textExt is a whitelist of common code/config text file extensions.
var textExt = map[string]struct{}{
	".txt": {}, ".md": {}, ".markdown": {},
	".go": {}, ".py": {}, ".js": {}, ".ts": {}, ".tsx": {}, ".jsx": {},
	".java": {}, ".c": {}, ".h": {}, ".cc": {}, ".cpp": {}, ".hpp": {},
	".rs": {}, ".rb": {}, ".php": {}, ".swift": {}, ".kt": {}, ".scala": {},
	".sh": {}, ".bash": {}, ".zsh": {}, ".fish": {}, ".ps1": {},
	".html": {}, ".htm": {}, ".css": {}, ".scss": {}, ".less": {},
	".json": {}, ".yaml": {}, ".yml": {}, ".toml": {}, ".ini": {}, ".cfg": {}, ".conf": {},
	".xml": {}, ".sql": {}, ".csv": {}, ".tsv": {}, ".log": {},
	".env": {}, ".gitignore": {}, ".dockerfile": {}, ".makefile": {},
	".vue": {}, ".svelte": {}, ".lua": {}, ".dart": {}, ".r": {}, ".m": {},
}

// RenderAttachmentText renders a text attachment into a string that can be
// appended to the task.
func RenderAttachmentText(att Attachment) string {
	return fmt.Sprintf("\n<attached_file path=%q name=%q>\n%s\n</attached_file>\n",
		att.Path, att.Name, att.Text)
}

// guessMediaKind infers the media category from mime type or filename extension
// when an explicit Kind is missing.
// Used to classify channel.Artifact / tools.Artifact products (which lack a Kind field)
// into image | video | audio | file, so the Channel side can choose the upload path.
func guessMediaKind(mime, name string) string {
	m := strings.ToLower(mime)
	switch {
	case strings.HasPrefix(m, "image/"):
		return "image"
	case strings.HasPrefix(m, "video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"):
		return "audio"
	}
	// Fallback by extension
	lower := strings.ToLower(name)
	switch {
	case hasAnySuffix(lower, ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg"):
		return "image"
	case hasAnySuffix(lower, ".mp4", ".mov", ".avi", ".mkv", ".webm"):
		return "video"
	case hasAnySuffix(lower, ".mp3", ".wav", ".m4a", ".aac", ".ogg", ".flac", ".silk", ".amr"):
		return "audio"
	}
	return "file"
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, sfx := range suffixes {
		if strings.HasSuffix(s, sfx) {
			return true
		}
	}
	return false
}