// Command machine-manager is the Herdr plugin that manages SSH machines.
// One binary serves three manifest entrypoints: the resident daemon that owns
// the machine cache and the job queue, the action that opens the popup, and
// the popup pane that runs the TUI. See docs/design.md.
package main

import (
	"context"
	"errors"
	"os"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-client/plugin"

	"github.com/vika2603/herdr-machine-manager/internal/config"
	"github.com/vika2603/herdr-machine-manager/internal/daemon"
	"github.com/vika2603/herdr-machine-manager/internal/ui"
)

// Entrypoint ids herdr-plugin.toml declares.
const (
	paneManager = "manager"
	actionOpen  = "open"
)

func main() {
	ctx, stop := plugin.ShutdownContext(context.Background())
	code := newPlugin().Run(ctx)
	stop()
	os.Exit(code)
}

func newPlugin() *plugin.Plugin {
	p := plugin.New()
	p.Startup(onStartup)
	p.Action(actionOpen, onOpen)
	p.Pane(paneManager, onManager)
	return p
}

// onStartup runs the daemon for as long as Herdr is up.
func onStartup(ctx context.Context, env *plugin.Env) error {
	return daemon.Run(ctx, env)
}

// onOpen opens the manager popup. Placement and size come from the manifest,
// so the call names only the plugin and the entrypoint.
func onOpen(ctx context.Context, env *plugin.Env) error {
	if err := daemon.Ensure(ctx, env); err != nil {
		return err
	}
	params := herdr.PluginPaneOpenParams{
		PluginID:   env.PluginID,
		Entrypoint: paneManager,
		Focus:      herdr.Ptr(true),
	}
	// A size in the user's config overrides the manifest's.
	if cfg, err := config.Load(env.ConfigDir); err == nil {
		if width, ok := config.ParseSize(cfg.PopupWidth); ok {
			params.Width = &width
		}
		if height, ok := config.ParseSize(cfg.PopupHeight); ok {
			params.Height = &height
		}
	}
	_, err := env.Client().PluginPaneOpen(ctx, params)
	return err
}

// onManager runs the TUI until the popup closes. A closed popup is a normal
// exit, not a failed plugin command.
func onManager(ctx context.Context, env *plugin.Env) error {
	err := ui.Run(ctx, env)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
