// Package skill installs the ladle Agent Skill for AI coding agents.
package skill

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed SKILL.md
var markdown string

// Markdown returns the embedded SKILL.md content.
func Markdown() string {
	return markdown
}

// Agent identifies a target AI coding agent.
type Agent string

const (
	// AgentClaude is Anthropic's Claude Code (Agent Skill format).
	AgentClaude Agent = "claude"
)

// SupportedAgents lists the agents that Install knows how to target.
var SupportedAgents = []Agent{AgentClaude}

// Scope selects where the skill is installed.
type Scope int

const (
	// ScopeUser installs into the user's home directory (global).
	ScopeUser Scope = iota
	// ScopeProject installs into the current working directory (per-project).
	ScopeProject
)

// Dir returns the directory the skill is installed into for the given agent and
// scope. The SKILL.md file lives directly inside it.
func Dir(agent Agent, scope Scope) (string, error) {
	switch agent {
	case AgentClaude:
		base, err := claudeBase(scope)
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "skills", "ladle"), nil
	default:
		return "", fmt.Errorf("unsupported agent %q (supported: claude)", agent)
	}
}

func claudeBase(scope Scope) (string, error) {
	if scope == ScopeProject {
		return ".claude", nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

// Install writes SKILL.md into the agent's skill directory and returns the path
// written. When force is false and the file already exists, it returns an error
// rather than overwriting.
func Install(agent Agent, scope Scope, force bool) (string, error) {
	dir, err := Dir(agent, scope)
	if err != nil {
		return "", err
	}
	dest := filepath.Join(dir, "SKILL.md")

	if !force {
		switch _, err := os.Stat(dest); {
		case err == nil:
			return "", fmt.Errorf("%s already exists (use --force to overwrite)", dest)
		case !errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("checking %s: %w", dest, err)
		}
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(dest, []byte(markdown), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", dest, err)
	}
	return dest, nil
}
