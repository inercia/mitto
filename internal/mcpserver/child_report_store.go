package mcpserver

import (
	"errors"
	"os"

	"github.com/inercia/mitto/internal/session"
)

const childReportsStateFile = "child-reports.json"

type persistedChildReportCollector struct {
	CurrentTaskID string                  `json:"current_task_id,omitempty"`
	Reports       map[string]*childReport `json:"reports"`
}

func loadChildReportCollector(store *session.Store, parentSessionID string) (*childReportCollector, error) {
	collector := &childReportCollector{
		parentSessionID: parentSessionID,
		reports:         make(map[string]*childReport),
		store:           store,
	}
	if store == nil {
		return collector, nil
	}
	var state persistedChildReportCollector
	err := store.ReadSessionSidecarJSON(parentSessionID, childReportsStateFile, &state)
	if errors.Is(err, os.ErrNotExist) {
		return collector, nil
	}
	if err != nil {
		return nil, err
	}
	collector.currentTaskID = state.CurrentTaskID
	if state.Reports != nil {
		collector.reports = state.Reports
	}
	return collector, nil
}

func (c *childReportCollector) persist() error {
	if c.store == nil {
		return nil
	}
	// Lock order is collector.mu before Store locks. Store code never calls back
	// into a collector, so persistence cannot invert this order.
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.WriteSessionSidecarJSON(c.parentSessionID, childReportsStateFile, persistedChildReportCollector{
		CurrentTaskID: c.currentTaskID,
		Reports:       c.reports,
	})
}

func (s *Server) deleteChildReportCollector(parentSessionID string) {
	s.childReportCollectorsMu.Lock()
	delete(s.childReportCollectors, parentSessionID)
	s.childReportCollectorsMu.Unlock()
}
