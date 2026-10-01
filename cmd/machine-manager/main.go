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

func main() {
	ctx, stop := plugin.ShutdownContext(context.Background())
	p := plugin.New()
	p.Startup(daemon.Run)
	p.Action("open", onOpen)
	p.Pane("manager", pane(ui.Run))
	p.Pane("prompt", pane(ui.RunPrompt))
	code := p.Run(ctx)
	stop()
	os.Exit(code)
}

func onOpen(ctx context.Context, env *plugin.Env) error {
	if err := daemon.Ensure(ctx, env); err != nil {
		return err
	}
	params := herdr.PluginPaneOpenParams{
		PluginID:   env.PluginID,
		Entrypoint: "manager",
		Focus:      herdr.Some(true),
	}
	// A size in the user's config overrides the manifest's.
	if cfg, err := config.Load(env.ConfigDir); err == nil {
		if width, ok := config.ParseSize(cfg.PopupWidth); ok {
			params.Width = herdr.Some(width)
		}
		if height, ok := config.ParseSize(cfg.PopupHeight); ok {
			params.Height = herdr.Some(height)
		}
	}
	_, err := env.Client().PluginPaneOpen(ctx, params)
	return err
}

func pane(run func(context.Context, *plugin.Env) error) func(context.Context, *plugin.Env) error {
	return func(ctx context.Context, env *plugin.Env) error {
		err := run(ctx, env)
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}
