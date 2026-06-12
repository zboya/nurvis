package modelmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// HFModelDetail is the slim projection of the HuggingFace single-model API
// (https://huggingface.co/api/models/{repo_id}) that we care about: the
// pipeline tag plus the free-form tag list. Other fields are ignored.
type HFModelDetail struct {
	ID           string   `json:"id"`
	ModelID      string   `json:"modelId"`
	PipelineTag  string   `json:"pipeline_tag"`
	Tags         []string `json:"tags"`
	LibraryName  string   `json:"library_name"`
	LastModified string   `json:"lastModified"`
}

// FetchModelDetail calls the HuggingFace single-model API for the given
// repository. The endpoint is unauthenticated for public models; if HF_TOKEN
// is set we forward it to lift rate limits.
func FetchModelDetail(ctx context.Context, repo string) (*HFModelDetail, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return nil, fmt.Errorf("modelmgr: empty repo")
	}
	url := fmt.Sprintf("https://huggingface.co/api/models/%s", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "nurvis-modelmgr/1.0")
	if token := os.Getenv("HF_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("hf detail HTTP %d: %s", resp.StatusCode, string(body))
	}
	var d HFModelDetail
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ModalitiesFromHF maps a HuggingFace `pipeline_tag` plus auxiliary tags into
// the simplified modality set we surface to the frontend: text / image /
// audio / video. Always includes "text" for any model whose pipeline involves
// reading or producing natural language. The returned slice is deduplicated
// and sorted in a stable order (text, image, audio, video).
//
// See internal/modelmgr/hf_tags.md for the canonical list of upstream tags.
func ModalitiesFromHF(pipelineTag string, tags []string) []string {
	pt := strings.ToLower(strings.TrimSpace(pipelineTag))
	tagSet := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		tagSet[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	hasTag := func(t string) bool {
		_, ok := tagSet[t]
		return ok
	}

	mods := map[string]bool{}

	// Pipeline-tag driven modality detection. The mapping follows hf_tags.md:
	// any pipeline that mentions "text" → text; "image" → image; "audio" or
	// "speech" → audio; "video" → video.
	switch pt {
	// ── pure text pipelines
	case "text-generation",
		"text-classification",
		"token-classification",
		"question-answering",
		"table-question-answering",
		"zero-shot-classification",
		"translation",
		"summarization",
		"feature-extraction",
		"fill-mask",
		"sentence-similarity",
		"text-ranking":
		mods["text"] = true

	// ── multimodal: text + image
	case "image-text-to-text",
		"visual-question-answering",
		"document-question-answering",
		"visual-document-retrieval":
		mods["text"] = true
		mods["image"] = true

	// ── multimodal: text + audio
	case "audio-text-to-text":
		mods["text"] = true
		mods["audio"] = true

	// ── multimodal: text + video
	case "video-text-to-text":
		mods["text"] = true
		mods["video"] = true

	// ── any-to-any: stay generous
	case "any-to-any":
		mods["text"] = true
		mods["image"] = true
		mods["audio"] = true
		mods["video"] = true

	// ── computer vision (no language)
	case "image-classification",
		"object-detection",
		"image-segmentation",
		"image-to-image",
		"unconditional-image-generation",
		"zero-shot-image-classification",
		"mask-generation",
		"zero-shot-object-detection",
		"image-feature-extraction",
		"keypoint-detection",
		"depth-estimation":
		mods["image"] = true
	case "image-to-text":
		mods["image"] = true
		mods["text"] = true

	// ── to image
	case "text-to-image":
		mods["text-to-image"] = true
	case "text-to-3d", "image-to-3d":
		mods["image"] = true
		if pt == "text-to-3d" {
			mods["text"] = true
		}

	// ── to video
	case "text-to-video":
		mods["text-to-video"] = true
	case "image-text-to-video":
		mods["image-text-to-video"] = true
	case "image-to-video":
		mods["image-to-video"] = true

	// ── to audio
	case "text-to-speech", "text-to-audio":
		mods["text-to-audio"] = true
	}

	// Tag-level fallbacks so we still classify models with empty/unusual
	// pipeline_tag (e.g. raw GGUF mirrors that only carry "gguf" + arch tags).
	if hasTag("multimodal") {
		mods["text"] = true
	}
	if hasTag("vision") || hasTag("clip") || hasTag("vqa") {
		mods["image"] = true
		mods["text"] = true
	}
	if hasTag("audio") || hasTag("speech") || hasTag("asr") || hasTag("tts") {
		mods["audio"] = true
	}
	if hasTag("video") {
		mods["video"] = true
	}
	// Default: anything that didn't match still likely speaks text.
	if len(mods) == 0 {
		mods["text"] = true
	}

	// Stable, deterministic ordering for downstream comparison and UI.
	order := []string{"text", "image", "audio", "video"}
	out := make([]string, 0, len(mods))
	for _, m := range order {
		if mods[m] {
			out = append(out, m)
		}
	}
	return out
}
