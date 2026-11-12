package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// StatusSnapshot is the cached store summary served by status.
type StatusSnapshot struct {
	Messages      int64 `json:"messages"`
	Facts         int64 `json:"facts"`
	Projects      int64 `json:"projects"`
	VerifiedFacts int64 `json:"verified_facts"`
	GoalsReady    int64 `json:"goals_ready"`
	GoalsInFlight int64 `json:"goals_in_flight"`
	Claims        int64 `json:"claims"`
}

func (s *Server) snapshot(now time.Time) StatusSnapshot {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	if !s.statusAt.IsZero() && now.Sub(s.statusAt) < 5*time.Second {
		return s.statusCache
	}
	if s.store == nil {
		return StatusSnapshot{}
	}
	var snap StatusSnapshot
	count := func(query string) int64 {
		var value int64
		if err := s.store.DB.QueryRow(query).Scan(&value); err == nil {
			return value
		}
		return 0
	}
	snap.Messages = count(`SELECT COUNT(*) FROM messages`)
	snap.Facts = count(`SELECT COUNT(*) FROM facts WHERE COALESCE(review_status, '') != 'archived'`)
	snap.Projects = count(`SELECT COUNT(*) FROM projects`)
	snap.VerifiedFacts = count(`SELECT COUNT(*) FROM facts WHERE file_refs_exist = 1 AND COALESCE(review_status, '') != 'archived'`)
	snap.Claims = count(`SELECT COUNT(*) FROM goal_claims WHERE status = 'active'`)
	snap.GoalsInFlight = snap.Claims
	s.statusCache = snap
	s.statusAt = now
	return snap
}

func (s *Server) invalidateStatus() {
	s.statusMu.Lock()
	s.statusAt = time.Time{}
	s.statusMu.Unlock()
}

var _ = store.Fact{}

func (s *Server) serveSubscription(ctx context.Context, writer *bufio.Writer, request Request) {
	var params struct {
		Kinds []string `json:"kinds"`
	}
	if len(request.Params) > 0 {
		_ = json.Unmarshal(request.Params, &params)
	}
	sub := s.bus.Subscribe(params.Kinds...)
	defer sub.Close()
	for {
		select {
		case event, ok := <-sub.Events():
			if !ok {
				return
			}
			encoded, err := json.Marshal(map[string]any{"event": event})
			if err != nil {
				return
			}
			if _, err := writer.Write(append(encoded, '\n')); err != nil {
				return
			}
			if err := writer.Flush(); err != nil {
				return
			}
		case <-ctx.Done():
			return
		}
	}
}
