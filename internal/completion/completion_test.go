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
				"cp skill s3:// gs:// az:// r2:// ssm://",
				`"$cur" != *://*`,
				`--complete-path`,
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
