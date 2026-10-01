package sshconfig

import (
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

// Resolve fills Host, User and Port from `ssh -G <name>` for the given
// aliases. It is safe to call for a subset, and an alias ssh cannot resolve is
// returned unchanged.
func Resolve(ctx context.Context, aliases []Alias) []Alias {
	resolved := slices.Clone(aliases)
	var wg sync.WaitGroup
	for worker := range min(resolveParallel, len(resolved)) {
		wg.Go(func() {
			for i := worker; i < len(resolved); i += resolveParallel {
				runCtx, cancel := context.WithTimeout(ctx, resolveTimeout)
				out, err := exec.CommandContext(runCtx, "ssh", "-G", resolved[i].Name).Output()
				cancel()
				if err == nil {
					apply(&resolved[i], out)
				}
			}
		})
	}
	wg.Wait()
	return resolved
}

// apply reads the `key value` lines of ssh -G output, whose keys are
// lowercase.
func apply(alias *Alias, out []byte) {
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " ")
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
