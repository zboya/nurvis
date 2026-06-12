package skill

import "testing"

func TestParseSkillMD_StandardFrontmatter(t *testing.T) {
	src := []byte(`---
name: hello
description: A friendly skill
version: 1.0.0
allowed-tools: [exec, read_file]
---
# Hello

This is the body.
`)
	fm, body, err := parseSkillMD(src)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.Name != "hello" {
		t.Errorf("name: got %q want hello", fm.Name)
	}
	if fm.Description != "A friendly skill" {
		t.Errorf("desc: got %q", fm.Description)
	}
	if fm.Version != "1.0.0" {
		t.Errorf("version: got %q", fm.Version)
	}
	if len(fm.AllowedTools) != 2 || fm.AllowedTools[0] != "exec" || fm.AllowedTools[1] != "read_file" {
		t.Errorf("allowed-tools: got %v", fm.AllowedTools)
	}
	if body == "" || body[0] != '#' {
		t.Errorf("body: got %q", body)
	}
}

func TestParseSkillMD_NoFrontmatter(t *testing.T) {
	src := []byte(`# Hello

just body, no frontmatter.
`)
	fm, body, err := parseSkillMD(src)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.Name != "" || fm.Description != "" {
		t.Errorf("expected empty frontmatter, got %+v", fm)
	}
	if body == "" {
		t.Errorf("body should be non-empty")
	}
}

func TestParseSkillMD_QuotedDescription(t *testing.T) {
	src := []byte(`---
name: foo
description: "Quoted, with comma"
---
body
`)
	fm, _, err := parseSkillMD(src)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if fm.Description != "Quoted, with comma" {
		t.Errorf("desc: got %q", fm.Description)
	}
}

func TestParseSkillMD_UnclosedFrontmatter(t *testing.T) {
	src := []byte(`---
name: broken
description: oops
`)
	if _, _, err := parseSkillMD(src); err == nil {
		t.Errorf("expected error for unclosed frontmatter")
	}
}
