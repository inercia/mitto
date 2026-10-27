package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// handleChildrenTasksHistory uses only the authenticated caller's collector.
// Filters cannot select a namespace; retained entry ownership is authoritative
// even when the child's current metadata no longer exists.
func (s *Server) handleChildrenTasksHistory(ctx context.Context, req *mcp.CallToolRequest, input ChildrenTasksHistoryInput) (*mcp.CallToolResult, ChildrenTasksHistoryOutput, error) {
	out := ChildrenTasksHistoryOutput{Entries: []ChildReportHistoryEntry{}}
	if input.SelfID == "" {
		out.Error = "self_id is required"
		return nil, out, nil
	}
	realSessionID := s.resolveSelfIDWithMCPContext(ctx, input.SelfID, req)
	if realSessionID == "" {
		out.Error = "self_id could not be resolved"
		return nil, out, nil
	}
	if s.getSession(realSessionID) == nil {
		out.Error = "session not found or not running"
		return nil, out, nil
	}
	s.mu.RLock()
	store := s.store
	s.mu.RUnlock()
	if store == nil {
		out.Error = "session store not available"
		return nil, out, nil
	}
	limit := 5
	if input.Limit != nil {
		limit = *input.Limit
	}
	switch {
	case limit < 1 || limit > 20:
		out.Error = "limit must be between 1 and 20 (default 5)"
	case input.TaskID != nil && *input.TaskID == "":
		out.Error = "task_id must be nonempty when supplied; omit it for all tasks"
	case input.Association != nil && *input.Association != string(childReportExplicit) && *input.Association != string(childReportInferred) && *input.Association != string(childReportUnassociated):
		out.Error = "association must be explicit, inferred, or unassociated"
	case input.ReportID != nil && input.BeforeReportID != nil:
		out.Error = "report_id and before_report_id cannot be combined"
	}
	if out.Error != "" {
		return nil, out, nil
	}
	c, err := s.getOrCreateCollector(realSessionID)
	if err != nil {
		out.Error = fmt.Sprintf("failed to load child report history: %v", err)
		return nil, out, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Locate IDs in the caller's unfiltered append order, never another sidecar.
	end := len(c.entries)
	if input.ReportID != nil || input.BeforeReportID != nil {
		id := input.ReportID
		if id == nil {
			id = input.BeforeReportID
		}
		position := -1
		for i := range c.entries {
			if c.entries[i].ID == *id {
				position = i
				break
			}
		}
		if position == -1 {
			out.Error = "report ID not found in your history"
			return nil, out, nil
		}
		if input.BeforeReportID != nil {
			end = position
		} else {
			end = position + 1
		}
	}
	for i := end - 1; i >= 0; i-- {
		entry := c.entries[i]
		if (input.ReportID != nil && entry.ID != *input.ReportID) ||
			(input.ChildID != "" && entry.ChildID != input.ChildID) ||
			(input.TaskID != nil && entry.EffectiveTaskID != *input.TaskID) ||
			(input.Association != nil && string(entry.Association) != *input.Association) {
			continue
		}
		if len(out.Entries) == limit {
			out.HasMore = true
			out.NextBeforeReportID = out.Entries[len(out.Entries)-1].ReportID
			break
		}
		out.Entries = append(out.Entries, ChildReportHistoryEntry{
			ReportID: entry.ID, ChildID: entry.ChildID, ReceivedAt: entry.ReceivedAt.Format(time.RFC3339Nano),
			SuppliedTaskID: entry.SuppliedTaskID, EffectiveTaskID: entry.EffectiveTaskID,
			Association: string(entry.Association), RawReport: string(entry.Report),
		})
	}
	out.Success = true
	return nil, out, nil
}
