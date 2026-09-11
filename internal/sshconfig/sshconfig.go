// Package sshconfig enumerates the connectable host aliases of an OpenSSH
// client configuration and resolves their effective connection details.
package sshconfig

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

// Alias is one connectable host alias from the SSH client configuration.
type Alias struct {
	Name   string `json:"name"`   // the alias as written on the Host line
	Source string `json:"source"` // absolute path of the file it was declared in
	Host   string `json:"host"`   // effective hostname from ssh -G, empty until resolved
	User   string `json:"user"`
	Port   string `json:"port"`
}

// maxIncludeDepth bounds Include recursion so that configurations including
// each other terminate.
const maxIncludeDepth = 16

// DefaultPath returns the path of the per-user client configuration.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

// Aliases parses path and every file it includes, returning the connectable
// aliases in first-seen order.
func Aliases(path string) ([]Alias, error) {
	p := &parser{seen: make(map[string]struct{})}
	if err := p.parseFile(path, 0); err != nil {
		return nil, err
	}
	return p.aliases, nil
}

type parser struct {
	aliases []Alias
	seen    map[string]struct{}
}

func (p *parser) parseFile(path string, depth int) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}

	// A Match block states conditions for the options that follow; it declares
	// no alias, and this parser does not evaluate its conditions. The block
	// runs until the next Host or Match line.
	inMatch := false

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		keyword, rest := splitKeyword(line)
		switch strings.ToLower(keyword) {
		case "host":
			inMatch = false
			for _, pattern := range splitArgs(rest) {
				p.add(pattern, abs)
			}
		case "match":
			inMatch = true
		case "include":
			if inMatch {
				continue
			}
			for _, arg := range splitArgs(rest) {
				p.include(arg, filepath.Dir(abs), depth)
			}
		}
	}
	return scanner.Err()
}

func (p *parser) add(pattern, source string) {
	if !connectable(pattern) {
		return
	}
	if _, ok := p.seen[pattern]; ok {
		return
	}
	p.seen[pattern] = struct{}{}
	p.aliases = append(p.aliases, Alias{Name: pattern, Source: source})
}

func (p *parser) include(arg, dir string, depth int) {
	if depth >= maxIncludeDepth {
		return
	}
	pattern := expandTilde(arg)
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(dir, pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return
	}
	for _, match := range matches {
		// An included file that cannot be read is skipped rather than failing
		// the enumeration, the way ssh tolerates an Include that matches
		// nothing.
		_ = p.parseFile(match, depth+1)
	}
}

// connectable reports whether a Host pattern names a single host to connect
// to. Wildcard and negated patterns only carry options for other hosts.
func connectable(pattern string) bool {
	return pattern != "" && !strings.ContainsAny(pattern, "*?!")
}

// splitKeyword separates the keyword from its arguments, accepting both
// "Keyword value" and "Keyword=value".
func splitKeyword(line string) (keyword, rest string) {
	i := strings.IndexAny(line, " \t=")
	if i < 0 {
		return line, ""
	}
	rest = strings.TrimLeft(line[i:], " \t")
	rest = strings.TrimLeft(strings.TrimPrefix(rest, "="), " \t")
	return line[:i], rest
}

func splitArgs(s string) []string {
	var (
		args   []string
		cur    strings.Builder
		quoted bool
		begun  bool
	)
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			begun = true
		case !quoted && (r == ' ' || r == '\t'):
			if begun {
				args = append(args, cur.String())
				cur.Reset()
				begun = false
			}
		default:
			cur.WriteRune(r)
			begun = true
		}
	}
	if begun {
		args = append(args, cur.String())
	}
	return args
}

func expandTilde(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~"))
}
