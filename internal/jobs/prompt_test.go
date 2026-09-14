package jobs

import "testing"

func TestDescribePrompt(t *testing.T) {
	tests := []struct {
		name string
		text string
		want PromptInfo
	}{
		{"default no", "Continue? [y/N] ", PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n"}},
		{"default yes", "Continue? [Y/n] ", PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n", DefaultYes: true}},
		{"word choices", "Continue? (yes/no)", PromptInfo{Kind: PromptConfirm, Yes: "yes", No: "no"}},
		{"word choices in brackets", "Continue? [yes/no]", PromptInfo{Kind: PromptConfirm, Yes: "yes", No: "no"}},
		{"known install", "continue installing the remote herdr binary?", PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n"}},
		{"actual install asset", `Install the 0.9.0 stable asset for linux-x86_64 to "$HOME/.local/bin/herdr"? [Y/n]`, PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n", DefaultYes: true}},
		{"known replace", "stop the incompatible remote server?", PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n"}},
		{"ansi", "\x1b[33mContinue? [y/N]\x1b[0m", PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n"}},
		{"password", "user@host's password: ", PromptInfo{Kind: PromptSecret}},
		{"passphrase", "Enter passphrase for key '/tmp/key':", PromptInfo{Kind: PromptSecret}},
		{"secret takes precedence", "Save password? [y/N]", PromptInfo{Kind: PromptSecret}},
		{"unknown question", "Which port?", PromptInfo{Kind: PromptText}},
		{"progress", "Installing remote package...", PromptInfo{Kind: PromptText}},
		{"empty", "", PromptInfo{Kind: PromptText}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DescribePrompt(tt.text); got != tt.want {
				t.Errorf("DescribePrompt(%q) = %+v, want %+v", tt.text, got, tt.want)
			}
		})
	}
}
