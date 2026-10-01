package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       Config
		wantErr    bool
	}{
		{name: "missing", want: Default()},
		{
			name: "partial", body: `popup_width = "40%"`,
			want: Config{PopupWidth: "40%", InstallRemote: true, Notifications: true},
		},
		{
			name: "all fields",
			body: "popup_width = \"70%\"\npopup_height = 24\ninstall_remote = false\nssh_config = \"~/other\"\nnotifications = false\n",
			want: Config{PopupWidth: "70%", PopupHeight: "24", SSHConfig: "~/other"},
		},
		{name: "malformed", body: "popup_width = ", want: Default(), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.name != "missing" {
				if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tt.body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got, err := Load(dir)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("Load = %+v, %v; want %+v, error=%v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	for _, tt := range []struct {
		in      Size
		ok      bool
		percent uint8
		cells   uint16
	}{
		{"55%", true, 55, 0}, {" 80 % ", true, 80, 0}, {"24", true, 0, 24},
		{"", false, 0, 0}, {"0%", false, 0, 0}, {"101%", false, 0, 0},
		{"0", false, 0, 0}, {"wide", false, 0, 0},
	} {
		got, ok := ParseSize(tt.in)
		if ok != tt.ok || got.Percent != tt.percent || got.Cells != tt.cells {
			t.Errorf("ParseSize(%q) = %+v, %v; want %d%% or %d cells, ok=%v", tt.in, got, ok, tt.percent, tt.cells, tt.ok)
		}
	}
}
