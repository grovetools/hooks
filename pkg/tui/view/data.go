// Package view: data adapter helpers between the daemon session feed
// and the bubbletea Model. Anything in this file should be a pure
// function over models.Session slices — no bubbletea, no rendering, no
// view state. The intent is to keep model.go focused on update/view
// logic and isolate "what shape does the session list take" decisions
// here so they can be unit-tested in isolation and so future fetch
// paths (SSE, polling, initial construction) all run sessions through
// the same normalization.
package view

import "github.com/grovetools/core/pkg/models"

// normalizeSessions collapses sessions that share a JobFilePath down to
// the single most-recently-active entry, returning the deduped slice
// and a map keyed by the surviving session's ID with the total number
// of runs that were folded into it (always >= 1 for entries that have
// a JobFilePath).
//
// Why dedupe at all: the daemon legitimately tracks each agent spawn
// as its own session row, so a job that has been retried 16 times
// produces 16 rows for the same .md file. Showing all 16 in the
// browser tree is noise — the user wants to see the job, not every
// previous attempt. The surviving entry's run count is surfaced in the
// row label as "(×N)" so the historical attempts are at least
// acknowledged in the UI even though only the latest is selectable.
// Future work: a key to expand a collapsed entry into its individual
// runs (the older entries are dropped here, not stored, so that work
// would need to retain them).
//
// Sessions without a JobFilePath are passed through unchanged and
// contribute no entries to the run-count map. Order of the surviving
// sessions is preserved relative to the input.
func normalizeSessions(in []*models.Session) ([]*models.Session, map[string]int) {
	if len(in) == 0 {
		return in, nil
	}
	bestIdx := make(map[string]int, len(in))
	counts := make(map[string]int, len(in))
	for i, s := range in {
		if s.JobFilePath == "" {
			continue
		}
		counts[s.JobFilePath]++
		if cur, ok := bestIdx[s.JobFilePath]; ok {
			if in[i].LastActivity.After(in[cur].LastActivity) {
				bestIdx[s.JobFilePath] = i
			}
			continue
		}
		bestIdx[s.JobFilePath] = i
	}
	out := make([]*models.Session, 0, len(in))
	runCounts := make(map[string]int, len(bestIdx))
	for i, s := range in {
		if s.JobFilePath == "" {
			out = append(out, s)
			continue
		}
		if bestIdx[s.JobFilePath] == i {
			out = append(out, s)
			runCounts[s.ID] = counts[s.JobFilePath]
		}
	}
	return out, runCounts
}
