package daemon

import (
	"context"
	"log"
	"time"

	"github.com/vika2603/herdr-client/herdr"

	"github.com/vika2603/herdr-machine-manager/internal/jobs"
)

const (
	attentionTimeout = 3 * time.Second
	// Herdr acknowledges pane.open before the new TUI subscribes to the daemon.
	attentionOpenGrace = 2 * time.Second
)

// requestAttention opens a compact prompt popup when a background job needs an
// answer and no plugin popup is open. One prompt only gets one attempt:
// closing the reopened popup must not make repeated prompt updates reopen it.
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
	if d.server.HasSubscribers() {
		d.attentionMu.Unlock()
		return
	}
	if d.attentionOpening || time.Now().Before(d.attentionOpeningUntil) {
		if d.attentionPending == nil {
			d.attentionPending = make(map[string]jobs.Job)
		}
		d.attentionPending[job.ID] = job
		if !d.attentionReplayActive {
			d.attentionReplayActive = true
			go d.replayAttention()
		}
		d.attentionMu.Unlock()
		return
	}
	d.launchAttentionLocked(job)
	d.attentionMu.Unlock()
}

// launchAttentionLocked starts the RPC without holding up the job queue.
func (d *Daemon) launchAttentionLocked(job jobs.Job) {
	d.attentionOpening = true
	go func() {
		// The user may have opened a plugin popup after the job update was sent.
		d.attentionMu.Lock()
		prompt, stillWaiting := d.attentionPrompts[job.ID]
		if !stillWaiting || prompt != job.Prompt || d.server.HasSubscribers() {
			d.attentionOpening = false
			d.attentionMu.Unlock()
			return
		}
		d.attentionMu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), attentionTimeout)
		defer cancel()
		params := herdr.PluginPaneOpenParams{
			PluginID:   d.env.PluginID,
			Entrypoint: "prompt",
			Focus:      herdr.Ptr(true),
		}
		open := d.attentionOpen
		if open == nil {
			open = func(ctx context.Context, params herdr.PluginPaneOpenParams) error {
				_, err := d.env.Client().PluginPaneOpen(ctx, params)
				return err
			}
		}
		err := open(ctx, params)
		d.attentionMu.Lock()
		d.attentionOpening = false
		if err == nil {
			d.attentionOpeningUntil = time.Now().Add(attentionOpenGrace)
		} else if d.attentionPrompts[job.ID] == job.Prompt {
			// A later update may retry if Herdr was only temporarily unavailable.
			delete(d.attentionPrompts, job.ID)
		}
		d.attentionMu.Unlock()
		if err != nil {
			log.Printf("daemon: cannot open prompt for waiting job %s: %v", job.ID, err)
			d.notifyAttentionOpenFailure(job, err)
		}
	}()
}

// replayAttention keeps a second job's prompt from disappearing during the
// short interval between pane.open returning and the new TUI subscribing.
func (d *Daemon) replayAttention() {
	for {
		d.attentionMu.Lock()
		if len(d.attentionPending) == 0 || d.server.HasSubscribers() {
			clear(d.attentionPending)
			d.attentionReplayActive = false
			d.attentionMu.Unlock()
			return
		}
		wait := time.Until(d.attentionOpeningUntil)
		if d.attentionOpening {
			wait = 50 * time.Millisecond
		}
		if wait > 0 {
			d.attentionMu.Unlock()
			time.Sleep(wait)
			continue
		}
		var job jobs.Job
		for id, pending := range d.attentionPending {
			job = pending
			delete(d.attentionPending, id)
			break
		}
		d.launchAttentionLocked(job)
		d.attentionMu.Unlock()
	}
}

func (d *Daemon) notifyAttentionOpenFailure(job jobs.Job, openErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), attentionTimeout)
	defer cancel()
	_, err := d.env.Client().NotificationShow(ctx, herdr.NotificationShowParams{
		Title: "SSH machines: input needed",
		Body:  herdr.Ptr(job.Title + " — open the manager manually (" + openErr.Error() + ")"),
	})
	if err != nil {
		log.Printf("daemon: cannot notify about waiting job %s: %v", job.ID, err)
	}
}

// clearAttention lets a later prompt from the same job request attention,
// including when its wording is identical to the previous one.
func (d *Daemon) clearAttention(jobID string) {
	d.attentionMu.Lock()
	delete(d.attentionPrompts, jobID)
	delete(d.attentionPending, jobID)
	d.attentionMu.Unlock()
}
