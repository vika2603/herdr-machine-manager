package jobs

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// PromptKind describes the input a stopped command is asking for.
type PromptKind string

const (
	PromptText    PromptKind = "text"
	PromptConfirm PromptKind = "confirm"
	PromptSecret  PromptKind = "secret"
)

// PromptInfo describes a prompt without making a decision on the user's behalf.
// Yes and No are the tokens accepted by a recognized confirmation prompt.
type PromptInfo struct {
	Kind       PromptKind
	Yes        string
	No         string
	DefaultYes bool
}

var (
	secretInputPrompt  = regexp.MustCompile(`(?i)\b(password|passphrase|secret|token)\b`)
	choicePrompt       = regexp.MustCompile(`(?i)[\[(]\s*(yes|y)\s*/\s*(no|n)\s*[\])]\s*$`)
	installQuestion    = regexp.MustCompile(`(?i)\binstall(?:ing)? the remote herdr binary\?\s*$`)
	stopServerQuestion = regexp.MustCompile(`(?i)\bstop\b[^?]*\bserver\b[^?]*\?\s*$`)
)

// DescribePrompt classifies known terminal input requests. Unrecognized text
// stays a text prompt: a question mark alone does not imply yes/no semantics.
func DescribePrompt(text string) PromptInfo {
	clean := strings.TrimSpace(ansi.Strip(text))
	if secretInputPrompt.MatchString(clean) {
		return PromptInfo{Kind: PromptSecret}
	}
	if choice := choicePrompt.FindStringSubmatch(clean); choice != nil {
		return PromptInfo{
			Kind:       PromptConfirm,
			Yes:        strings.ToLower(choice[1]),
			No:         strings.ToLower(choice[2]),
			DefaultYes: choice[1] == strings.ToUpper(choice[1]),
		}
	}
	if installQuestion.MatchString(clean) || stopServerQuestion.MatchString(clean) {
		return PromptInfo{Kind: PromptConfirm, Yes: "y", No: "n"}
	}
	return PromptInfo{Kind: PromptText}
}
