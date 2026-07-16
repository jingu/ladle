package completion

import (
	"bytes"
	"strings"
	"testing"
)

func TestGeneratedCompletionIncludesCopyAndURICompletion(t *testing.T) {
	tests := []struct {
		name     string
		shell    Shell
		contains []string
	}{
		{
			name:  "bash",
			shell: ShellBash,
			contains: []string{
				"cp skill s3:// gs:// az:// ssm://",
				`"$cur" != *://*`,
				`--complete-path`,
				`for word in "${COMP_WORDS[@]:1}"`,
				`schemes="s3:// gs:// az://"`,
			},
		},
		{
			name:  "zsh",
			shell: ShellZsh,
			contains: []string{
				"cp:Copy an object with its metadata",
				`"${words[CURRENT]}" == *://*`,
				"_ladle_uri",
				"ssm://:AWS Systems Manager Parameter URI",
				`for word in "${words[@]}"`,
			},
		},
		{
			name:  "fish",
			shell: ShellFish,
			contains: []string{
				"__fish_use_subcommand",
				"-a cp",
				"__ladle_complete_uri",
				"'ssm://'",
				"if contains -- cp $tokens",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var script bytes.Buffer
			if err := Generate(&script, tt.shell); err != nil {
				t.Fatalf("Generate(%q): %v", tt.shell, err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(script.String(), want) {
					t.Errorf("generated %s completion does not contain %q", tt.shell, want)
				}
			}
		})
	}
}

func TestBashCompletionChecksOptionValuesBeforeCopyURIs(t *testing.T) {
	var script bytes.Buffer
	if err := Generate(&script, ShellBash); err != nil {
		t.Fatalf("Generate(%q): %v", ShellBash, err)
	}
	output := script.String()
	optionValues := strings.Index(output, `case "${prev}" in`)
	copyURIs := strings.Index(output, `for word in "${COMP_WORDS[@]:1}"`)
	if optionValues < 0 || copyURIs < 0 {
		t.Fatalf("generated bash completion is missing option or copy URI handling")
	}
	if optionValues > copyURIs {
		t.Error("copy URI completion runs before option-value completion")
	}
}

func TestGeneratedCompletionsExcludeUnsupportedR2(t *testing.T) {
	for _, shell := range []Shell{ShellBash, ShellZsh, ShellFish} {
		t.Run(string(shell), func(t *testing.T) {
			var script bytes.Buffer
			if err := Generate(&script, shell); err != nil {
				t.Fatalf("Generate(%q): %v", shell, err)
			}
			if strings.Contains(script.String(), "r2://") {
				t.Errorf("generated %s completion advertises unsupported r2://", shell)
			}
		})
	}
}
