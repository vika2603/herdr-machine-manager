package sshconfig

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	resolveTimeout  = 5 * time.Second
	resolveParallel = 8
)

// runSSHG is a variable so tests can resolve aliases without running ssh.
var runSSHG = func(ctx context.Context, name string) ([]byte, error) {
	return exec.CommandContext(ctx, "ssh", "-G", name).Output()
}

// Resolve fills Host, User and Port from `ssh -G <name>` for the given
// aliases. It is safe to call for a subset, and an alias ssh cannot resolve is
// returned unchanged.
func Resolve(ctx context.Context, aliases []Alias) []Alias {
	resolved := slices.Clone(aliases)
	slots := make(chan struct{}, resolveParallel)
	var wg sync.WaitGroup
	for i := range resolved {
		wg.Add(1)
		go func(alias *Alias) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()

			runCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
			defer cancel()
			out, err := runSSHG(runCtx, alias.Name)
			if err != nil {
				return
			}
			apply(alias, out)
		}(&resolved[i])
	}
	wg.Wait()
	return resolved
}

// apply reads the `key value` lines of ssh -G output, whose keys are
// lowercase.
func apply(alias *Alias, out []byte) {
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "hostname":
			alias.Host = value
		case "user":
			alias.User = value
		case "port":
			alias.Port = value
		}
	}
}
