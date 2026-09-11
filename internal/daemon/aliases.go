package daemon

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/vika2603/herdr-machine-manager/internal/sshconfig"
)

// aliasTTL bounds how long a parsed ~/.ssh/config is reused. The file is also
// re-read whenever its modification time changes.
const aliasTTL = 30 * time.Second

func (d *Daemon) aliasList(ctx context.Context) AliasesResult {
	path := d.cfg.SSHConfigPath()
	if path == "" {
		var err error
		if path, err = sshconfig.DefaultPath(); err != nil {
			return AliasesResult{Error: err.Error()}
		}
	}

	var mod time.Time
	if info, statErr := os.Stat(path); statErr == nil {
		mod = info.ModTime()
	}

	d.mu.Lock()
	fresh := d.aliases != nil && d.aliasMod.Equal(mod) && time.Since(d.aliasRead) < aliasTTL
	cached := append([]sshconfig.Alias(nil), d.aliases...)
	d.mu.Unlock()
	if fresh {
		return AliasesResult{Aliases: cached}
	}

	parsed, err := sshconfig.Aliases(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AliasesResult{}
		}
		return AliasesResult{Error: err.Error()}
	}
	resolved := sshconfig.Resolve(ctx, parsed)

	d.mu.Lock()
	d.aliases = resolved
	d.aliasRead = time.Now()
	d.aliasMod = mod
	d.mu.Unlock()
	return AliasesResult{Aliases: resolved}
}
