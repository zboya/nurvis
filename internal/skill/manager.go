// Package skill manages loading and on-demand reading of Skill packages.
//
// A Skill (inspired by Anthropic Agent Skills) is a "directive + script + resource"
// directory package that declares its capabilities via SKILL.md (with YAML frontmatter).
// At runtime it follows a "progressive disclosure" pattern:
//
//  1. Load phase: scan all enabled skill SKILL.md files, caching only metadata
//     (name / description / allowed_tools / path). **No** tools are registered.
//
//  2. Prompt phase: based on skill_grants, inject the list of skills available
//     to the current agent (only name+description) into the system prompt's
//     <available_skills> section.
//
//  3. When the model decides to use a skill, it calls the built-in tool `use_skill`,
//     which returns the full SKILL.md content + resource listing from this Manager.
//     The model then uses generic exec / fs tools (with the working directory set
//     to the skill directory) to execute scripts/ and read references/.
//
// This approach avoids polluting the tool namespace, keeps context consumption minimal
// (only name+description, a few dozen tokens), and is compatible with community
// skill package formats.
package skill

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/zboya/nurvis/internal/store/repo"
)

// SkillRecord model is defined in the repo package; type alias here for backward compatibility.
type SkillRecord = repo.SkillRecord

// Loaded represents a skill instance loaded into memory.
type Loaded struct {
	ID           string   // database primary key
	Name         string   // SKILL.md frontmatter.name (name exposed to the model)
	Description  string   // brief description, injected into prompt
	Version      string   // optional
	Path         string   // absolute path to the skill directory
	AllowedTools []string // tool whitelist declared in SKILL.md (optional, used as hard constraint)
	Body         string   // SKILL.md body (excluding frontmatter); returned by use_skill
}

// Manager manages skill loading and on-demand reading.
type Manager struct {
	repo *repo.SkillRepo

	mu       sync.RWMutex
	byName   map[string]*Loaded // name → Loaded
	byID     map[string]*Loaded // id   → Loaded
	grantsFn func(ctx context.Context, agentID string) (map[string]struct{}, error)
}

// NewManager creates a new Skill Manager.
func NewManager(db *sql.DB) *Manager {
	r := repo.NewSkillRepo(db)
	m := &Manager{
		repo:   r,
		byName: make(map[string]*Loaded),
		byID:   make(map[string]*Loaded),
	}
	// Default: query via grant table; tests can inject a mock.
	m.grantsFn = func(ctx context.Context, agentID string) (map[string]struct{}, error) {
		ids, err := r.ListGrantedSkillIDs(ctx, agentID)
		if err != nil {
			return nil, err
		}
		set := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			set[id] = struct{}{}
		}
		return set, nil
	}
	return m
}

// Load reads all enabled skills from the database and parses their SKILL.md files,
// caching only metadata. Failed skills are skipped with a warn log; loading is not aborted.
func (m *Manager) Load(ctx context.Context) error {
	records, err := m.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("skill: list: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.byName = make(map[string]*Loaded, len(records))
	m.byID = make(map[string]*Loaded, len(records))

	loaded := 0
	for _, rec := range records {
		if !rec.Enabled {
			continue
		}
		l, err := m.loadOne(rec)
		if err != nil {
			slog.Warn("skill: load failed", "skill", rec.Name, "path", rec.Path, "err", err)
			continue
		}
		m.byName[l.Name] = l
		m.byID[l.ID] = l
		loaded++
	}
	slog.Info("skill: loaded", "count", loaded)
	return nil
}

// loadOne parses the SKILL.md in a single skill directory.
// Backward compatible: if SKILL.md does not exist but manifest.json does, falls back to manifest fields.
func (m *Manager) loadOne(rec SkillRecord) (*Loaded, error) {
	skillMdPath := filepath.Join(rec.Path, "SKILL.md")
	if data, err := os.ReadFile(skillMdPath); err == nil {
		fm, body, err := parseSkillMD(data)
		if err != nil {
			return nil, fmt.Errorf("parse SKILL.md: %w", err)
		}
		name := fm.Name
		if name == "" {
			name = rec.Name
		}
		return &Loaded{
			ID:           rec.ID,
			Name:         name,
			Description:  fm.Description,
			Version:      firstNonEmpty(fm.Version, rec.Version),
			Path:         rec.Path,
			AllowedTools: fm.AllowedTools,
			Body:         body,
		}, nil
	}

	// Backward compatible: legacy manifest.json (only name/description extracted).
	if data, err := os.ReadFile(filepath.Join(rec.Path, "manifest.json")); err == nil {
		fm, body, err := parseLegacyManifest(data)
		if err == nil {
			return &Loaded{
				ID:          rec.ID,
				Name:        firstNonEmpty(fm.Name, rec.Name),
				Description: fm.Description,
				Version:     firstNonEmpty(fm.Version, rec.Version),
				Path:        rec.Path,
				Body:        body,
			}, nil
		}
	}

	return nil, fmt.Errorf("neither SKILL.md nor manifest.json found in %s", rec.Path)
}

// Get looks up a loaded skill by name.
func (m *Manager) Get(name string) (*Loaded, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.byName[name]
	return l, ok
}

// ListForAgent returns the list of skills authorized for the given agent (for prompt injection).
// If agentID is empty, all loaded skills are returned (for system debugging / default scenarios).
func (m *Manager) ListForAgent(ctx context.Context, agentID string) ([]*Loaded, error) {
	m.mu.RLock()
	all := make([]*Loaded, 0, len(m.byName))
	for _, l := range m.byName {
		all = append(all, l)
	}
	m.mu.RUnlock()

	if agentID == "" {
		return all, nil
	}
	grants, err := m.grantsFn(ctx, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]*Loaded, 0, len(grants))
	for _, l := range all {
		if _, ok := grants[l.ID]; ok {
			out = append(out, l)
		}
	}
	return out, nil
}

// IsGrantedForAgent checks whether a skill name is visible to the specified agent.
// If agentID is empty, always returns true (system / debugging scenario).
func (m *Manager) IsGrantedForAgent(ctx context.Context, agentID, skillName string) (bool, *Loaded) {
	l, ok := m.Get(skillName)
	if !ok {
		return false, nil
	}
	if agentID == "" {
		return true, l
	}
	grants, err := m.grantsFn(ctx, agentID)
	if err != nil {
		return false, nil
	}
	_, ok = grants[l.ID]
	return ok, l
}

// LookupForAgent returns skill metadata for the app layer to adapt as tools.SkillProvider.
//
// Returns: granted = whether authorized; info = metadata for the use_skill tool; ok = whether the skill exists.
// Note: this does not directly implement the tools.SkillProvider interface (different return types);
// the app layer performs a lightweight adaptation.
func (m *Manager) LookupForAgent(ctx context.Context, agentID, skillName string) (granted bool, info LookupInfo, ok bool) {
	l, ok2 := m.Get(skillName)
	if !ok2 {
		return false, LookupInfo{}, false
	}
	info = LookupInfo{
		Name:        l.Name,
		Description: l.Description,
		Body:        l.Body,
		Dir:         l.Path,
	}
	if agentID == "" {
		return true, info, true
	}
	grants, err := m.grantsFn(ctx, agentID)
	if err != nil {
		return false, info, true
	}
	_, granted = grants[l.ID]
	return granted, info, true
}

// LookupInfo corresponds to the fields of tools.SkillInfo. The tools package uses it
// via structural equivalence (duck typing via small interface); an independent type is
// defined here to avoid a reverse dependency.
type LookupInfo struct {
	Name        string
	Description string
	Body        string
	Dir         string
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
