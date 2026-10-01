package daemon

import (
	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

// Active is derived from Herdr's list, never persisted separately.
type Connection struct {
	store.Connection
	Active bool `json:"active"`
}

func (d *Daemon) reconcile(actual []machines.Machine) []Connection {
	stored := d.store.List()
	taken := make(map[string]bool, len(actual))
	out := make([]Connection, 0, len(stored))

	// Reserve known endpoint IDs before trying the target/label fallback.
	// Otherwise an offline duplicate can claim another connection's endpoint.
	byID := make(map[string]machines.Machine, len(actual))
	for _, machine := range actual {
		byID[machine.ID] = machine
	}
	matched := make(map[string]machines.Machine, len(stored))
	for _, conn := range stored {
		if machine, ok := byID[conn.ProfileID]; ok && conn.ProfileID != "" && !taken[machine.ID] {
			matched[conn.ID] = machine
			taken[machine.ID] = true
		}
	}

	for _, conn := range stored {
		machine, ok := matched[conn.ID]
		if !ok {
			for _, candidate := range actual {
				if !taken[candidate.ID] && candidate.Target == conn.Target && candidate.Label == conn.Label {
					machine, ok = candidate, true
					break
				}
			}
		}
		if ok {
			taken[machine.ID] = true
		}
		if conn.ProfileID != machine.ID {
			conn.ProfileID = machine.ID
			if saved, err := d.store.Put(conn); err == nil {
				conn = saved
			}
		}
		out = append(out, Connection{Connection: conn, Active: ok})
	}

	for _, machine := range actual {
		if taken[machine.ID] {
			continue
		}
		adopted, err := d.store.Put(store.Connection{
			Label:     machine.Label,
			Target:    machine.Target,
			Session:   machine.Session,
			ProfileID: machine.ID,
		})
		if err != nil {
			continue
		}
		out = append(out, Connection{Connection: adopted, Active: true})
	}
	return out
}
