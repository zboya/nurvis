package skill

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Frontmatter holds the parsed result of the YAML header at the top of SKILL.md.
// Only a minimal YAML subset is supported (key: value, list [a, b]) to avoid a yaml dependency.
type Frontmatter struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Version      string   `json:"version"`
	AllowedTools []string `json:"allowed-tools"`
}

// parseSkillMD parses SKILL.md and returns the frontmatter and body.
//
// Two formats are supported:
//  1. Standard frontmatter: starts with `---\n` and closes with `\n---\n`, containing simplified YAML.
//  2. No frontmatter: the entire content is treated as the body; all frontmatter fields are empty.
func parseSkillMD(data []byte) (Frontmatter, string, error) {
	var fm Frontmatter
	src := string(bytes.TrimLeft(data, "\ufeff \t\r\n"))

	if !strings.HasPrefix(src, "---") {
		return fm, src, nil
	}
	// Skip the opening --- line
	rest := strings.TrimPrefix(src, "---")
	rest = strings.TrimLeft(rest, "\r\n")

	end := strings.Index(rest, "\n---")
	if end < 0 {
		return fm, src, fmt.Errorf("SKILL.md: frontmatter not closed by ---")
	}
	yamlBlock := rest[:end]
	body := rest[end+len("\n---"):]
	body = strings.TrimLeft(body, "\r\n")

	if err := parseSimpleYAML(yamlBlock, &fm); err != nil {
		return fm, body, err
	}
	return fm, body, nil
}

// parseSimpleYAML parses a minimal YAML subset (key: value, key: [a, b]) into Frontmatter.
// Does not support nesting, multi-line strings, anchors, or other advanced features —
// sufficient for SKILL.md use cases.
func parseSimpleYAML(s string, fm *Frontmatter) error {
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		colon := strings.Index(trimmed, ":")
		if colon < 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:colon]))
		val := strings.TrimSpace(trimmed[colon+1:])
		val = strings.Trim(val, `"'`)

		switch key {
		case "name":
			fm.Name = val
		case "description":
			fm.Description = val
		case "version":
			fm.Version = val
		case "allowed-tools", "allowed_tools":
			fm.AllowedTools = parseListLiteral(val)
		}
	}
	return sc.Err()
}

// parseListLiteral parses a simple list in the form "[a, b, c]" or "a, b, c".
func parseListLiteral(s string) []string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"'`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseLegacyManifest is backward compatible with the legacy manifest.json format,
// extracting only name/description/version. The description is also returned as the
// body so the model can at least see something.
func parseLegacyManifest(data []byte) (Frontmatter, string, error) {
	var raw struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Frontmatter{}, "", err
	}
	return Frontmatter{
		Name:        raw.Name,
		Description: raw.Description,
		Version:     raw.Version,
	}, raw.Description, nil
}
