package store

import (
	"time"

	"github.com/jadmadi/sila/internal/loaders"
)

// RecordHookBatch persists a batch of normalized hook messages into the store.
// Satisfies loaders.DrainTarget interface.
func (s *Store) RecordHookBatch(tool string, msgs []loaders.Message) (newRecords, merged int, err error) {
	loadMsgs := make([]LoadMessage, len(msgs))
	for i, m := range msgs {
		parsedTS := m.TS.UTC().Format(time.RFC3339)
		loadMsgs[i] = LoadMessage{
			ProjectID:      m.ProjectDir,
			ContentHash:    ContentHash(m.Role, m.Content),
			TS:             parsedTS,
			Role:           m.Role,
			Content:        m.Content,
			Tool:           tool,
			SessionID:      m.SessionID,
			SourceLocation: m.SourceLocation,
			SourceOrder:    m.SourceOrder,
		}
	}
	return s.WriteRunScoped("", tool, tool, "", loadMsgs)
}
