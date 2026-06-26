package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkdownHasFrontmatter(t *testing.T) {
	md := Markdown()
	if !strings.HasPrefix(md, "---\n") {
		t.Fatalf("SKILL.md should start with YAML frontmatter, got: %.20q", md)
	}
	if !strings.Contains(md, "name: ladle") {
		t.Error("SKILL.md frontmatter should declare name: ladle")
	}
	if !strings.Contains(md, "description:") {
		t.Error("SKILL.md frontmatter should declare a description")
	}
}

func TestDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	tests := []struct {
		name  string
		agent Agent
		scope Scope
		want  string
	}{
		{"claude user", AgentClaude, ScopeUser, filepath.Join(home, ".claude", "skills", "ladle")},
		{"claude project", AgentClaude, ScopeProject, filepath.Join(".claude", "skills", "ladle")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Dir(tt.agent, tt.scope)
			if err != nil {
				t.Fatalf("Dir: %v", err)
			}
			if got != tt.want {
				t.Errorf("Dir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDirUnsupportedAgent(t *testing.T) {
	if _, err := Dir("cursor", ScopeUser); err == nil {
		t.Error("expected error for unsupported agent")
	}
}

func TestInstallProjectScope(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	dest, err := Install(AgentClaude, ScopeProject, false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}

	want := filepath.Join(".claude", "skills", "ladle", "SKILL.md")
	if dest != want {
		t.Errorf("dest = %q, want %q", dest, want)
	}

	got, err := os.ReadFile(filepath.Join(dir, want))
	if err != nil {
		t.Fatalf("reading installed skill: %v", err)
	}
	if string(got) != Markdown() {
		t.Error("installed content does not match embedded SKILL.md")
	}
}

func TestInstallNoOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, err := Install(AgentClaude, ScopeProject, false); err != nil {
		t.Fatalf("first Install: %v", err)
	}

	if _, err := Install(AgentClaude, ScopeProject, false); err == nil {
		t.Error("expected error when installing over an existing file without --force")
	}

	if _, err := Install(AgentClaude, ScopeProject, true); err != nil {
		t.Errorf("force Install should overwrite: %v", err)
	}
}

func TestInstallUnsupportedAgent(t *testing.T) {
	if _, err := Install("gemini", ScopeProject, false); err == nil {
		t.Error("expected error for unsupported agent")
	}
}

// TestInstallStatError checks that a non-NotExist stat failure (here ENOTDIR,
// because the skill directory path is occupied by a regular file) is surfaced
// rather than silently falling through to WriteFile.
func TestInstallStatError(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if err := os.MkdirAll(filepath.Join(".claude", "skills"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// Occupy the would-be "ladle" directory with a file, so stat of
	// .claude/skills/ladle/SKILL.md fails with ENOTDIR (not NotExist).
	if err := os.WriteFile(filepath.Join(".claude", "skills", "ladle"), []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := Install(AgentClaude, ScopeProject, false); err == nil {
		t.Error("expected error when stat of destination fails with a non-NotExist error")
	}
}

// chdir switches into dir for the duration of the test, restoring the previous
// working directory afterward.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}
