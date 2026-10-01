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

// Reconnect is one job, so disconnect cannot be admitted or cancelled separately.
func (d *Daemon) enqueue(conn store.Connection, kind jobs.Kind, install, reconnect bool) string {
	return d.queue.Submit(conn.ID, jobs.Spec{Kind: kind, Title: string(kind) + " " + conn.Label,
		Run: func(ctx context.Context, sink jobs.Sink) error {
			switch kind {
			case jobs.KindConnect:
				current, _ := d.store.Get(conn.ID)
				if reconnect && current.ProfileID != "" {
					if err := d.cli.Remove(ctx, current.ProfileID); err != nil {
						return err
					}
				}
				bin, err := d.cli.Binary()
				if err != nil {
					return err
				}
				return jobs.Exec(ctx, append([]string{bin}, d.cli.AddArgs(conn.Target, conn.Label, conn.Session)...), addAnswers(install), sink)
			case jobs.KindRename:
				return d.cli.Rename(ctx, conn.ProfileID, conn.Label)
			default:
				current := conn
				if kind == jobs.KindForget {
					current, _ = d.store.Get(conn.ID)
				}
				if current.ProfileID != "" {
					if err := d.cli.Remove(ctx, current.ProfileID); err != nil {
						return err
					}
				}
				if kind == jobs.KindForget {
					return d.store.Delete(conn.ID)
				}
				return nil
			}
		},
	}).ID
}

// Installation explicitly disabled by the caller is the only prompt answered
// here. All other questions, including stopping an incompatible remote server,
// move to awaiting_input for the user to decide.
var (
	installPrompt      = regexp.MustCompile(`(?i)install(ing)? the remote herdr binary\?`)
	installAssetPrompt = regexp.MustCompile(`(?i)\binstall the [0-9][^\r\n?]*\basset for [a-z0-9_-]+ to ["']?[^"'\r\n?]*/herdr["']?\?`)
)

func addAnswers(install bool) []jobs.Answer {
	if install {
		return nil
	}
	return []jobs.Answer{
		{Match: installPrompt, Reply: "n\n"},
		{Match: installAssetPrompt, Reply: "n\n"},
	}
}

func (d *Daemon) onJobUpdate(job jobs.Job) {
	d.broadcast(ipc.EventJobUpdated, job)
	d.requestAttention(job)
	if job.State == jobs.StateAwaitingInput {
		if d.cfg.Notifications {
			d.notify("SSH machines: input needed", job.Title+" — "+job.Prompt)
		}
		return
	}
	if !job.State.Terminal() {
		return
	}
	d.refresh(context.Background())
	if !d.cfg.Notifications {
		return
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = d.env.Client().NotificationShow(ctx, herdr.NotificationShowParams{
		Title: title,
		Body:  herdr.Some(body),
	})
}
