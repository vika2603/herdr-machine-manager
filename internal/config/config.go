// Package config reads the user's settings for the plugin. The file is
// config.toml in the directory herdr assigns the plugin
// (HERDR_PLUGIN_CONFIG_DIR); it belongs to the user, so it is only ever read.
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

// FileName is the configuration file inside the plugin config directory.
const FileName = "config.toml"

// Config is the user's settings, with the defaults already applied.
type Config struct {
	// PopupWidth and PopupHeight override the size declared in the manifest.
	PopupWidth  Size `toml:"popup_width"`
	PopupHeight Size `toml:"popup_height"`
	// InstallRemote is the default of the "install herdr on the remote" switch.
	InstallRemote bool `toml:"install_remote"`
	// SSHConfig is where the aliases are read from.
	SSHConfig string `toml:"ssh_config"`
	// Notifications sends a herdr toast when a job finishes or needs an answer.
	Notifications bool `toml:"notifications"`
}

// Default is the configuration used when the file is absent or a field is
// left out.
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

// UnmarshalTOML accepts a quoted percentage and a bare cell count.
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
	if value == "" {
		return herdr.PopupSize{}, false
	}
	if percent, ok := strings.CutSuffix(value, "%"); ok {
		n, err := strconv.Atoi(strings.TrimSpace(percent))
		if err != nil || n < 1 || n > 100 {
			return herdr.PopupSize{}, false
		}
		return herdr.PopupSize{Percent: uint8(n)}, true
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 65535 {
		return herdr.PopupSize{}, false
	}
	return herdr.PopupSize{Cells: uint16(n)}, true
}

// SSHConfigPath is the alias source, with ~ expanded. An empty setting leaves
// the caller to use the OpenSSH default.
func (c Config) SSHConfigPath() string {
	path := strings.TrimSpace(c.SSHConfig)
	if path == "" {
		return ""
	}
	// Only ~ and ~/… are ours to expand; ~user names another account's home.
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}
