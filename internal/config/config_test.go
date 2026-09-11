package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileKeepsDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg != Default() {
		t.Errorf("Load = %+v, want the defaults %+v", cfg, Default())
	}
}

func TestLoadReadsFields(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `
popup_width = "70%"
popup_height = 24
install_remote = false
ssh_config = "~/.ssh/other"
notifications = false
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PopupWidth != "70%" || cfg.PopupHeight != "24" {
		t.Errorf("popup size = %q x %q, want 70%% x 24", cfg.PopupWidth, cfg.PopupHeight)
	}
	if cfg.InstallRemote || cfg.Notifications {
		t.Errorf("booleans = %+v, want both false", cfg)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := cfg.SSHConfigPath(), filepath.Join(home, ".ssh/other"); got != want {
		t.Errorf("SSHConfigPath = %q, want %q", got, want)
	}
}

func TestLoadPartialFileKeepsOtherDefaults(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "popup_width = \"40%\"\n")
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.InstallRemote || !cfg.Notifications {
		t.Errorf("an unset field lost its default: %+v", cfg)
	}
}

func TestLoadMalformedFileReports(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "popup_width = \n")
	cfg, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted a malformed file")
	}
	if cfg != Default() {
		t.Errorf("a malformed file left %+v, want the defaults", cfg)
	}
}

func TestSize(t *testing.T) {
	tests := []struct {
		in      Size
		ok      bool
		percent uint8
		cells   uint16
	}{
		{in: "55%", ok: true, percent: 55},
		{in: " 80 % ", ok: true, percent: 80},
		{in: "80%", ok: true, percent: 80},
		{in: "24", ok: true, cells: 24},
		{in: "", ok: false},
		{in: "0%", ok: false},
		{in: "101%", ok: false},
		{in: "0", ok: false},
		{in: "wide", ok: false},
	}
	for _, tt := range tests {
		got, ok := ParseSize(tt.in)
		if ok != tt.ok {
			t.Errorf("Size(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			continue
		}
		if ok && (got.Percent != tt.percent || got.Cells != tt.cells) {
			t.Errorf("Size(%q) = %+v, want percent %d cells %d", tt.in, got, tt.percent, tt.cells)
		}
	}
}

func TestSSHConfigPathEmpty(t *testing.T) {
	if got := Default().SSHConfigPath(); got != "" {
		t.Errorf("SSHConfigPath = %q, want empty so the caller uses the OpenSSH default", got)
	}
}

func write(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSSHConfigPathLeavesOtherUsersHomeAlone(t *testing.T) {
	cfg := Config{SSHConfig: "~bob/.ssh/config"}
	if got := cfg.SSHConfigPath(); got != "~bob/.ssh/config" {
		t.Errorf("SSHConfigPath = %q, want it untouched: ~bob names another account", got)
	}
}
