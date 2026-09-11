package daemon

import (
	"context"

	"github.com/vika2603/herdr-machine-manager/internal/machines"
	"github.com/vika2603/herdr-machine-manager/internal/store"
)

// Connection is a stored connection plus whether herdr currently holds it.
// Active is derived from herdr's own list rather than stored, so the two can
// never disagree: the plugin uses `machine add` and `machine remove` and
// nothing else of herdr's machine state.
type Connection struct {
	store.Connection
	Active bool `json:"active"`
}

// reconcile merges the store with what herdr reports. It records the herdr
// endpoint id of a connection that is active, clears it for one that is not,
// and adopts a machine added outside the plugin so that it is manageable here
// instead of invisible.
func (d *Daemon) reconcile(actual []machines.Machine) []Connection {
	stored := d.store.List()
	taken := make(map[string]bool, len(actual))
	out := make([]Connection, 0, len(stored))

	for _, conn := range stored {
		machine, ok := matchMachine(actual, conn, taken)
		if ok {
			taken[machine.ID] = true
			if conn.ProfileID != machine.ID {
				conn.ProfileID = machine.ID
				if saved, err := d.store.Put(conn); err == nil {
					conn = saved
				}
			}
			out = append(out, Connection{Connection: conn, Active: true})
			continue
		}
		if conn.ProfileID != "" {
			conn.ProfileID = ""
			if saved, err := d.store.Put(conn); err == nil {
				conn = saved
			}
		}
		out = append(out, Connection{Connection: conn, Active: false})
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

// matchMachine pairs a stored connection with a herdr machine, by endpoint id
// when one is recorded and by target and label otherwise, which is what a
// freshly added machine matches on before its id is known.
func matchMachine(actual []machines.Machine, conn store.Connection, taken map[string]bool) (machines.Machine, bool) {
	if conn.ProfileID != "" {
		for _, machine := range actual {
			if machine.ID == conn.ProfileID && !taken[machine.ID] {
				return machine, true
			}
		}
	}
	for _, machine := range actual {
		if !taken[machine.ID] && machine.Target == conn.Target && machine.Label == conn.Label {
			return machine, true
		}
	}
	return machines.Machine{}, false
}

// connection returns one merged connection.
func (d *Daemon) connection(id string) (Connection, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, conn := range d.conns {
		if conn.ID == id {
			return conn, true
		}
	}
	return Connection{}, false
}

// reloadOnce reads herdr's list and reconciles it. The caller holds d.reload.
func (d *Daemon) reloadOnce(ctx context.Context) []Connection {
	list, err := d.cli.List(ctx)
	if err != nil {
		d.mu.Lock()
		d.cacheErr = err.Error()
		d.mu.Unlock()
		return nil
	}
	conns := d.reconcile(list)
	d.mu.Lock()
	d.cacheErr = ""
	d.conns = conns
	d.mu.Unlock()
	return conns
}
