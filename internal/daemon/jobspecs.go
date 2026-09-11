package daemon

import (
	"context"
	"regexp"
	"time"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-machine-manager/internal/ipc"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

// connectSpec adds the connection to herdr. It is the one job that needs a
// PTY: the command prepares the remote host and can ask questions.
func (d *Daemon) connectSpec(conn store.Connection, install bool) jobs.Spec {
	cli := d.cli
	bin, err := cli.Binary()
	if err != nil {
		return jobs.Spec{
			Kind:   jobs.KindConnect,
			Title:  "connect " + conn.Label,
			ConnID: conn.ID,
			Run:    func(context.Context, jobs.Sink) error { return err },
		}
	}
	return jobs.Spec{
		Kind:    jobs.KindConnect,
		Title:   "connect " + conn.Label,
		ConnID:  conn.ID,
		Args:    append([]string{bin}, cli.AddArgs(conn.Target, conn.Label, conn.Session)...),
		Answers: addAnswers(install),
	}
}

// disconnectSpec removes the connection from herdr and keeps it in the store,
// which is what this plugin means by disabling one.
func (d *Daemon) disconnectSpec(conn store.Connection) jobs.Spec {
	cli, id := d.cli, conn.ProfileID
	return jobs.Spec{
		Kind:   jobs.KindDisconnect,
		Title:  "disconnect " + conn.Label,
		ConnID: conn.ID,
		Run: func(ctx context.Context, _ jobs.Sink) error {
			if id == "" {
				return nil
			}
			return cli.Remove(ctx, id)
		},
	}
}

func (d *Daemon) renameSpec(conn store.Connection) jobs.Spec {
	cli, id, label := d.cli, conn.ProfileID, conn.Label
	return jobs.Spec{
		Kind:   jobs.KindRename,
		Title:  "rename to " + label,
		ConnID: conn.ID,
		Run: func(ctx context.Context, _ jobs.Sink) error {
			return cli.Rename(ctx, id, label)
		},
	}
}

// forgetSpec drops the connection from herdr and from the store.
func (d *Daemon) forgetSpec(conn store.Connection) jobs.Spec {
	cli, connections := d.cli, d.store
	return jobs.Spec{
		Kind:   jobs.KindForget,
		Title:  "forget " + conn.Label,
		ConnID: conn.ID,
		Run: func(ctx context.Context, _ jobs.Sink) error {
			if current, ok := connections.Get(conn.ID); ok && current.ProfileID != "" {
				if err := cli.Remove(ctx, current.ProfileID); err != nil {
					return err
				}
			}
			return connections.Delete(conn.ID)
		},
	}
}

// Prompts herdr machine add is known to ask. Anything outside this set moves
// the job to awaiting_input rather than being answered on the user's behalf; a
// password prompt is never answered here.
var (
	installPrompt = regexp.MustCompile(`(?i)install(ing)? the remote herdr binary\?`)
	replacePrompt = regexp.MustCompile(`(?i)stop .*server.*\?`)
)

func addAnswers(install bool) []jobs.Answer {
	reply := "n\n"
	if install {
		reply = "y\n"
	}
	return []jobs.Answer{
		{Match: installPrompt, Reply: reply},
		// Replacing an incompatible remote server is herdr's own destructive
		// default-No question; the plugin keeps that default.
		{Match: replacePrompt, Reply: "n\n"},
	}
}

func (d *Daemon) onJobUpdate(job jobs.Job) {
	d.broadcast(ipc.EventJobUpdated, job)
	if job.State == jobs.StateAwaitingInput {
		d.notify("SSH machines: input needed", job.Title+" — "+job.Prompt)
		return
	}
	if !job.State.Terminal() {
		return
	}
	d.refresh(context.Background())
	switch job.State {
	case jobs.StateSucceeded:
		d.notify("SSH machines", job.Title+" finished")
	case jobs.StateFailed:
		d.notify("SSH machines: failed", job.Title+" — "+job.Err)
	}
}

func (d *Daemon) onJobOutput(id string, lines []string) {
	d.broadcast(ipc.EventJobOutput, map[string]any{"job_id": id, "lines": lines})
}

// notify goes through herdr's own toast, so the user sees a job finish with
// the delivery and position they already configured.
func (d *Daemon) notify(title, body string) {
	if !d.cfg.Notifications {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = d.env.Client().NotificationShow(ctx, herdr.NotificationShowParams{
		Title: title,
		Body:  herdr.Ptr(body),
	})
}
