// Package agent: tag inference helpers.
//
// AgentTag values:
//   to-text   — text-in / text-out, runs the standard llama agent loop
//   to-image  — text-or-image-in / image-out, routed through gosd
//   to-video  — text-or-image-in / video-out, routed through gosd
package agent

import (
	"context"
	"strings"
)

// Tag values understood by the runtime. Strings are persisted in agents.tag.
const (
	TagToText  = "to-text"
	TagToImage = "to-image"
	TagToVideo = "to-video"
)

// IsValidTag reports whether s is one of the recognised tag values.
func IsValidTag(s string) bool {
	switch s {
	case TagToText, TagToImage, TagToVideo:
		return true
	}
	return false
}

// InferAgentTag derives a tag from the model's HuggingFace metadata
// (pipeline_tag + tags + modelmgr's normalised modalities slice).
//
// Resolution order:
//  1. Explicit pipeline_tag → text-to-video / image-to-video → to-video.
//  2. Explicit pipeline_tag → text-to-image / image-to-image → to-image.
//  3. Modalities slice carries one of the above synthetic tokens
//     (modelmgr.ModalitiesFromHF emits them as-is for these pipelines).
//  4. Free-form tag list contains hints (text-to-image, video, …).
//  5. Fall back to to-text.
func InferAgentTag(pipelineTag string, tags, modalities []string) string {
	pt := strings.ToLower(strings.TrimSpace(pipelineTag))
	switch pt {
	case "text-to-video", "image-to-video", "image-text-to-video", "video-to-video":
		return TagToVideo
	case "text-to-image", "image-to-image":
		return TagToImage
	}

	for _, m := range modalities {
		switch strings.ToLower(strings.TrimSpace(m)) {
		case "text-to-video", "image-to-video", "image-text-to-video":
			return TagToVideo
		case "text-to-image":
			return TagToImage
		}
	}

	for _, t := range tags {
		lower := strings.ToLower(strings.TrimSpace(t))
		switch lower {
		case "text-to-video", "image-to-video":
			return TagToVideo
		case "text-to-image", "stable-diffusion", "diffusion":
			return TagToImage
		}
	}
	// Generic substring sniff (catches "text-to-image-finetuned" etc.).
	joined := strings.ToLower(strings.Join(append([]string{pipelineTag}, tags...), " "))
	switch {
	case strings.Contains(joined, "text-to-video"), strings.Contains(joined, "image-to-video"):
		return TagToVideo
	case strings.Contains(joined, "text-to-image"), strings.Contains(joined, "image-to-image"):
		return TagToImage
	}
	return TagToText
}

// modelMetaLookup is the minimal interface Manager uses to resolve a model's
// HF metadata (defined here so we don't pull in repo type just for the pull
// row). app/manager wiring satisfies this with an adapter over ModelRepo.
type modelMetaLookup interface {
	LookupModelMeta(ctx context.Context, model string) (pipelineTag string, tags, modalities []string, ok bool)
}
