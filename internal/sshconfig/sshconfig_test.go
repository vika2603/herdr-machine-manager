package sshconfig

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// wantAlias is an expected alias whose source is relative to testdata.
type wantAlias struct {
	name   string
	source string
}

func TestAliases(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []wantAlias
	}{
		{
			name: "several patterns on one host line",
			path: "patterns/config",
			want: []wantAlias{
				{"pi", "patterns/config"},
				{"proxy", "patterns/config"},
				{"proxy-tunnel", "patterns/config"},
				{"shared", "patterns/config"},
			},
		},
		{
			name: "keyword equals value",
			path: "equals/config",
			want: []wantAlias{
				{"equals-host", "equals/config"},
				{"spaced-host", "equals/config"},
				{"included-equals", "equals/inc/a.conf"},
			},
		},
		{
			name: "quoted patterns",
			path: "quoted/config",
			want: []wantAlias{
				{"quoted", "quoted/config"},
				{"quoted-first", "quoted/config"},
				{"plain-second", "quoted/config"},
			},
		},
		{
			name: "include glob nested and missing",
			path: "include/config",
			want: []wantAlias{
				{"first", "include/conf.d/10-first.conf"},
				{"deep", "include/conf.d/nested/deep.conf"},
				{"second", "include/conf.d/20-second.conf"},
				{"root-level", "include/config"},
			},
		},
		{
			name: "match block declares no alias",
			path: "match/config",
			want: []wantAlias{
				{"before-match", "match/config"},
				{"after-match", "match/config"},
			},
		},
		{
			name: "include cycle terminates",
			path: "cycle/a.conf",
			want: []wantAlias{
				{"cycle-a", "cycle/a.conf"},
				{"cycle-b", "cycle/b.conf"},
			},
		},
		{
			name: "duplicate keeps first source",
			path: "duplicates/config",
			want: []wantAlias{
				{"shared-alias", "duplicates/config"},
				{"only-in-extra", "duplicates/extra.conf"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Aliases(filepath.Join("testdata", tt.path))
			if err != nil {
				t.Fatalf("Aliases(%q) returned error: %v", tt.path, err)
			}
			want := make([]Alias, 0, len(tt.want))
			for _, w := range tt.want {
				source, err := filepath.Abs(filepath.Join("testdata", w.source))
				if err != nil {
					t.Fatalf("filepath.Abs(%q): %v", w.source, err)
				}
				want = append(want, Alias{Name: w.name, Source: source})
			}
			if !slices.Equal(got, want) {
				t.Errorf("Aliases(%q) = %+v, want %+v", tt.path, got, want)
			}
		})
	}
}

func TestAliasesMissingFile(t *testing.T) {
	if _, err := Aliases(filepath.Join(t.TempDir(), "config")); err == nil {
		t.Fatal("Aliases() on a missing file returned no error")
	}
}

func TestAliasesIncludeTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".ssh", "config"), "Include ~/extra/*.conf\n")
	writeFile(t, filepath.Join(home, "extra", "hosts.conf"), "Host from-home\n")

	got, err := Aliases(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		t.Fatalf("Aliases() returned error: %v", err)
	}
	want := []Alias{{Name: "from-home", Source: filepath.Join(home, "extra", "hosts.conf")}}
	if !slices.Equal(got, want) {
		t.Errorf("Aliases() = %+v, want %+v", got, want)
	}
}

func TestDefaultPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() returned error: %v", err)
	}
	if want := filepath.Join(home, ".ssh", "config"); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestSplitKeyword(t *testing.T) {
	tests := []struct {
		line    string
		keyword string
		rest    string
	}{
		{"Host pi", "Host", "pi"},
		{"Host=pi", "Host", "pi"},
		{"Host = pi", "Host", "pi"},
		{"Host\tpi other", "Host", "pi other"},
		{"Host", "Host", ""},
	}

	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			keyword, rest := splitKeyword(tt.line)
			if keyword != tt.keyword || rest != tt.rest {
				t.Errorf("splitKeyword(%q) = (%q, %q), want (%q, %q)", tt.line, keyword, rest, tt.keyword, tt.rest)
			}
		})
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}
