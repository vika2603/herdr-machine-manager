package sshconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

// stubRunSSHG replaces the ssh -G call for the duration of the test.
func stubRunSSHG(t *testing.T, run func(ctx context.Context, name string) ([]byte, error)) {
	t.Helper()
	original := runSSHG
	runSSHG = run
	t.Cleanup(func() { runSSHG = original })
}

func TestResolve(t *testing.T) {
	outputs := map[string]string{
		"pi":     "user alice\nhostname 10.0.0.5\nport 22\nidentityfile ~/.ssh/id_ed25519\n",
		"github": "hostname ssh.github.com\n",
	}

	tests := []struct {
		name    string
		aliases []Alias
		want    []Alias
	}{
		{
			name:    "fills host user and port",
			aliases: []Alias{{Name: "pi", Source: "/c"}},
			want:    []Alias{{Name: "pi", Source: "/c", Host: "10.0.0.5", User: "alice", Port: "22"}},
		},
		{
			name:    "output without user or port leaves them empty",
			aliases: []Alias{{Name: "github", Source: "/c"}},
			want:    []Alias{{Name: "github", Source: "/c", Host: "ssh.github.com"}},
		},
		{
			name:    "unresolvable alias is returned unchanged",
			aliases: []Alias{{Name: "unknown", Source: "/c", Host: "kept"}},
			want:    []Alias{{Name: "unknown", Source: "/c", Host: "kept"}},
		},
		{
			name:    "subset of a configuration",
			aliases: []Alias{{Name: "github", Source: "/c"}, {Name: "pi", Source: "/c"}},
			want: []Alias{
				{Name: "github", Source: "/c", Host: "ssh.github.com"},
				{Name: "pi", Source: "/c", Host: "10.0.0.5", User: "alice", Port: "22"},
			},
		},
		{
			name:    "no aliases",
			aliases: nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubRunSSHG(t, func(_ context.Context, name string) ([]byte, error) {
				out, ok := outputs[name]
				if !ok {
					return nil, errors.New("exit status 255")
				}
				return []byte(out), nil
			})

			got := Resolve(context.Background(), tt.aliases)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Resolve() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveDoesNotMutateInput(t *testing.T) {
	stubRunSSHG(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("hostname example.com\n"), nil
	})

	aliases := []Alias{{Name: "a", Source: "/c"}}
	Resolve(context.Background(), aliases)
	if aliases[0].Host != "" {
		t.Errorf("Resolve() modified its argument: %+v", aliases[0])
	}
}

func TestResolvePassesDeadline(t *testing.T) {
	stubRunSSHG(t, func(ctx context.Context, _ string) ([]byte, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("no deadline")
		}
		return []byte("hostname example.com\n"), nil
	})

	got := Resolve(context.Background(), []Alias{{Name: "a"}})
	if got[0].Host != "example.com" {
		t.Errorf("Resolve() = %+v, want the alias resolved under a deadline", got[0])
	}
}

func TestResolveBoundsConcurrency(t *testing.T) {
	var (
		mu      sync.Mutex
		running int
		peak    int
	)
	stubRunSSHG(t, func(_ context.Context, name string) ([]byte, error) {
		mu.Lock()
		running++
		if running > peak {
			peak = running
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			running--
			mu.Unlock()
		}()
		return []byte("hostname " + name + ".example.com\n"), nil
	})

	aliases := make([]Alias, 3*resolveParallel)
	want := make([]Alias, len(aliases))
	for i := range aliases {
		aliases[i] = Alias{Name: fmt.Sprintf("host%02d", i)}
		want[i] = Alias{Name: aliases[i].Name, Host: aliases[i].Name + ".example.com"}
	}

	got := Resolve(context.Background(), aliases)
	if !slices.Equal(got, want) {
		t.Errorf("Resolve() = %+v, want %+v", got, want)
	}
	if peak > resolveParallel {
		t.Errorf("Resolve() ran %d calls at once, want at most %d", peak, resolveParallel)
	}
}
