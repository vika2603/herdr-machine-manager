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

// Aliases parses path and every file it includes, returning the connectable
// aliases in first-seen order.
func Aliases(path string) ([]Alias, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = "~/.ssh/config"
	}
	path, err := expandTilde(path)
	if err != nil {
		return nil, err
	}
	p := &parser{seen: make(map[string]bool)}
	if err := p.parseFile(path, 0); err != nil {
		return nil, err
	}
	return p.aliases, nil
}

type parser struct {
	aliases []Alias
	seen    map[string]bool
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
				if pattern != "" && !strings.ContainsAny(pattern, "*?!") && !p.seen[pattern] {
					p.seen[pattern] = true
					p.aliases = append(p.aliases, Alias{Name: pattern, Source: abs})
				}
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

func (p *parser) include(arg, dir string, depth int) {
	if depth >= maxIncludeDepth {
		return
	}
	pattern, err := expandTilde(arg)
	if err != nil {
		return
	}
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
	)
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
		case !quoted && (r == ' ' || r == '\t'):
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

func expandTilde(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~")), nil
}
