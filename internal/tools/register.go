// Package tools provides the built-in tool collection.
package tools

import (
	"github.com/zboya/nurvis/internal/preview"
	"github.com/zboya/nurvis/internal/store/repo"
)

// RegisterAll registers all built-in tools into the Registry.
// When previewRegistry is not nil, the web_preview tool is also registered.
// When creds is not nil, the publish_cloudflare_pages tool is registered.
// When chanDisp is not nil, the channel.send tool is registered.
// When cronMgr is not nil, the cron tool is registered.
func RegisterAll(
	r *Registry,
	previewRegistry *preview.Registry,
	localBaseURL string,
	creds *repo.SiteCredentialRepo,
	chanDisp ChannelDispatcher,
	cronMgr CronManager,
) {
	r.MustRegister(&Exec{})
	r.MustRegister(&FSRead{})
	r.MustRegister(&FSWrite{})
	r.MustRegister(&FSList{})
	r.MustRegister(&FSDelete{})
	r.MustRegister(&EditFile{})
	r.MustRegister(&Glob{})
	r.MustRegister(&Grep{})
	r.MustRegister(&HTTPFetch{})

	if previewRegistry != nil {
		r.MustRegister(NewWebPreview(previewRegistry, localBaseURL))
	}

	if creds != nil {
		r.MustRegister(NewPublishCFPages(creds))
	}

	if chanDisp != nil {
		r.MustRegister(NewChannelSend(chanDisp))
	}

	if cronMgr != nil {
		r.MustRegister(NewCronTool(cronMgr))
	}
}

// RegisterSkillTool registers the use_skill tool.
// Extracted separately because SkillProvider is only ready during the app wiring phase.
func RegisterSkillTool(r *Registry, p SkillProvider) {
	r.MustRegister(NewUseSkill(p))
}
