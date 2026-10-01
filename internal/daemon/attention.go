package daemon

import (
	"context"
	"log"
	"time"

	"github.com/vika2603/herdr-client/herdr"
	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

// requestAttention attempts to open a prompt once per waiting question. Jobs
// arriving during pane startup are included in its initial subscribed snapshot.
func (d *Daemon) requestAttention(job jobs.Job) {
	if job.State != jobs.StateAwaitingInput || d.server == nil {
		return
	}
	d.attentionMu.Lock()
	if d.attentionPrompts == nil {
		d.attentionPrompts = make(map[string]string)
	}
	if prompt, ok := d.attentionPrompts[job.ID]; ok && prompt == job.Prompt {
		d.attentionMu.Unlock()
		return
	}
	d.attentionPrompts[job.ID] = job.Prompt
	if d.attentionOpening || d.server.HasSubscribers() {
		d.attentionMu.Unlock()
		return
	}
	d.attentionOpening = true
	d.attentionMu.Unlock()
	go func() {
		defer func() {
			d.attentionMu.Lock()
			d.attentionOpening = false
			d.attentionMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, err := d.env.Client().PluginPaneOpen(ctx, herdr.PluginPaneOpenParams{
			PluginID: d.env.PluginID, Entrypoint: "prompt", Focus: herdr.Some(true),
		})
		if err == nil {
			return
		}
		log.Printf("daemon: cannot open prompt for waiting job %s: %v", job.ID, err)
		// An unavailable popup must remain discoverable even with routine job
		// notifications disabled. One toast covers all jobs coalesced into this open.
		ctx, cancelToast := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelToast()
		_, err = d.env.Client().NotificationShow(ctx, herdr.NotificationShowParams{
			Title: "SSH machines: input needed",
			Body:  herdr.Some("Open the manager to answer waiting jobs (" + err.Error() + ")"),
		})
		if err != nil {
			log.Printf("daemon: cannot notify about waiting jobs: %v", err)
		}
	}()
}

// Clearing on departure from awaiting_input permits a later question with
// identical wording, while updates to a dismissed question stay suppressed.
func (d *Daemon) clearAttention(jobID string) {
	d.attentionMu.Lock()
	delete(d.attentionPrompts, jobID)
	d.attentionMu.Unlock()
}
