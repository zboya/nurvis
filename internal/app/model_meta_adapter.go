// Package app: small adapters that expose internal repositories to other
// packages without leaking direct repo dependencies.
package app

import (
	"context"

	"github.com/zboya/nurvis/internal/store/repo"
)

// modelMetaAdapter implements agent.modelMetaLookup by reading the
// (pipeline_tag, tags, modalities) triple persisted on the models row.
//
// This lets agent.Manager.Create automatically tag a freshly created agent
// without forcing the agent package to depend on repo.ModelRepo at
// construction time.
type modelMetaAdapter struct {
	repo *repo.ModelRepo
}

// LookupModelMeta looks up the model in models and decodes the JSON
// columns. Returns ok=false on miss (so Manager.Create falls back to its
// default tag handling).
func (a *modelMetaAdapter) LookupModelMeta(ctx context.Context, model string) (string, []string, []string, bool) {
	if a == nil || a.repo == nil || model == "" {
		return "", nil, nil, false
	}
	p, err := a.repo.Get(ctx, model)
	if err != nil || p == nil {
		return "", nil, nil, false
	}
	tags := decodeStringSlice(p.Tags)
	mods := decodeStringSlice(p.Modalities)
	return p.PipelineTag, tags, mods, true
}

func decodeStringSlice(in []string) []string {
	if len(in) > 0 {
		return in
	}
	return nil
}
