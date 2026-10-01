package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/vika2603/herdr-client/herdr"
)

const FileName = "config.toml"

type Config struct {
	PopupWidth    Size   `toml:"popup_width"`
	PopupHeight   Size   `toml:"popup_height"`
	InstallRemote bool   `toml:"install_remote"`
	SSHConfig     string `toml:"ssh_config"`
	Notifications bool   `toml:"notifications"`
}

func Default() Config {
	return Config{
		InstallRemote: true,
		Notifications: true,
	}
}

// Load reads dir/config.toml. A missing file is not an error: the defaults
// stand. A malformed one is reported, because silently ignoring a file the
// user wrote would be worse than saying it is wrong.
func Load(dir string) (Config, error) {
	cfg := Default()
	if dir == "" {
		return cfg, nil
	}
	path := filepath.Join(dir, FileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Default(), fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

// Size is a popup dimension: a cell count or a percentage such as "55%".
// Both spellings are accepted in TOML, as herdr accepts both in a manifest.
type Size string

func (s *Size) UnmarshalTOML(value any) error {
	switch v := value.(type) {
	case string:
		*s = Size(v)
	case int64:
		*s = Size(strconv.FormatInt(v, 10))
	default:
		return fmt.Errorf("want a cell count or a percentage such as \"55%%\"")
	}
	return nil
}

// ParseSize converts a configured dimension. An empty or unparsable value
// returns false, which leaves the manifest's own size in place.
func ParseSize(size Size) (herdr.PopupSize, bool) {
	value := strings.TrimSpace(string(size))
	percent := strings.HasSuffix(value, "%")
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(value, "%")))
	if err != nil || n < 1 || n > 65535 || (percent && n > 100) {
		return herdr.PopupSize{}, false
	}
	if percent {
		return herdr.PopupSize{Percent: uint8(n)}, true
	}
	return herdr.PopupSize{Cells: uint16(n)}, true
}
