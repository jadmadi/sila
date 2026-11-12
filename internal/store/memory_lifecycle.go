package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Memory lifecycle states. current is the only state visible to default reads.
type MemoryState string

const (
	MemoryStateCurrent    MemoryState = "current"
	MemoryStateArchived   MemoryState = "archived"
	MemoryStateSuperseded MemoryState = "superseded"
)

// Memory lifecycle actions recorded in memory_lifecycle.
const (
	MemoryActionArchive   = "archive"
	MemoryActionRestore   = "restore"
	MemoryActionSupersede = "supersede"
)

// ArchiveAuthority is the explicit authority behind an archive transition.
type ArchiveAuthority string

const (
	ArchiveAuthorityUserRequest   ArchiveAuthority = "user_request"
	ArchiveAuthorityAgentVerified ArchiveAuthority = "agent_verified"
	ArchiveAuthoritySensitive     ArchiveAuthority = "sensitive"
)

// ArchiveReason is the recorded reason for an archive transition.
type ArchiveReason string

const (
	ArchiveReasonWrong         ArchiveReason = "wrong"
	ArchiveReasonOutdated      ArchiveReason = "outdated"
	ArchiveReasonDuplicate     ArchiveReason = "duplicate"
	ArchiveReasonSensitive     ArchiveReason = "sensitive"
	ArchiveReasonSuperseded    ArchiveReason = "superseded"
	ArchiveReasonUserRequested ArchiveReason = "user_requested"
	ArchiveReasonOther         ArchiveReason = "other"
)

// Lifecycle error codes. They are public contract: scripts branch on them.
const (
	MemoryCodeStateConflict     = "MEMORY_STATE_CONFLICT"
	MemoryCodeStateInvalid      = "MEMORY_STATE_INVALID"
	MemoryCodeArchiveAuthority  = "MEMORY_ARCHIVE_AUTHORITY"
	MemoryCodeRestoreWindow     = "MEMORY_RESTORE_WINDOW"
	MemoryCodeSensitiveRestore  = "MEMORY_SENSITIVE_RESTORE"
	MemoryCodeSupersededRestore = "MEMORY_SUPERSEDED_RESTORE"
	MemoryCodeDuplicateTarget   = "MEMORY_DUPLICATE_TARGET"
	MemoryCodeChainBroken       = "MEMORY_CHAIN_BROKEN"
	MemoryCodeHardDeleteRefused = "MEMORY_HARD_DELETE_REFUSED"
	MemoryCodeArchiveNote       = "MEMORY_ARCHIVE_NOTE"
	MemoryCodeArchiveProject    = "MEMORY_ARCHIVE_PROJECT"
)

// MemoryArchiveMaxNote bounds the stored archive note.
const MemoryArchiveMaxNote = 500

// MemoryArchiveUndoWindow is how long an agent-verified archive stays
// agent-restorable.
const MemoryArchiveUndoWindow = 24 * time.Hour

// MemoryLifecycleEvent is one append-only audit entry.
type MemoryLifecycleEvent struct {
	EventID   string `json:"event_id"`
	MemoryID  string `json:"memory_id"`
	ProjectID string `json:"project_id"`
	Action    string `json:"action"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	Authority string `json:"authority"`
	Reason    string `json:"reason"`
	Note      string `json:"note,omitempty"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
	PrevHash  string `json:"prev_hash"`
	EventHash string `json:"event_hash"`
}

// ArchiveRecord is the agent-facing projection of an archived or superseded
// memory. It never carries envelope content.
type ArchiveRecord struct {
	MemoryID        string `json:"memory_id"`
	ProjectID       string `json:"project_id"`
	Title           string `json:"title,omitempty"`
	Kind            string `json:"kind,omitempty"`
	State           string `json:"state"`
	ArchiveID       string `json:"archive_id,omitempty"`
	Authority       string `json:"authority,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Note            string `json:"note,omitempty"`
	Actor           string `json:"actor,omitempty"`
	CreatedAt       string `json:"created_at,omitempty"`
	RestoreDeadline string `json:"restore_deadline,omitempty"`
	AgentRestorable bool   `json:"agent_restorable"`
	Restorable      bool   `json:"restorable"`
	Sensitive       bool   `json:"sensitive"`
}

// RedactSensitive returns a copy with title and note removed for sensitive
// archives. Callers that cannot prove human authority use it before rendering.
func (r ArchiveRecord) RedactSensitive() ArchiveRecord {
	if !r.Sensitive {
		return r
	}
	r.Title = "[redacted]"
	r.Note = ""
	return r
}

// RestoreResult reports one restore attempt. Restored is false for an
// idempotent no-op on an already-current memory.
type RestoreResult struct {
	MemoryID         string `json:"memory_id"`
	Restored         bool   `json:"restored"`
	State            string `json:"state"`
	Deadline         string `json:"deadline,omitempty"`
	Authority        string `json:"authority,omitempty"`
	ArchiveAuthority string `json:"archive_authority,omitempty"`
}

// MemoryHistory is the ordered lifecycle chain for one memory.
type MemoryHistory struct {
	MemoryID      string                 `json:"memory_id"`
	ProjectID     string                 `json:"project_id"`
	Events        []MemoryLifecycleEvent `json:"events"`
	Verified      bool                   `json:"verified"`
	BrokenEventID string                 `json:"broken_event_id,omitempty"`
}

// ArchiveStats summarizes archived and superseded records without content.
type ArchiveStats struct {
	Archived        int            `json:"archived"`
	Superseded      int            `json:"superseded"`
	ByReason        map[string]int `json:"by_reason"`
	ByAuthority     map[string]int `json:"by_authority"`
	Restorable      int            `json:"restorable"`
	AgentRestorable int            `json:"agent_restorable"`
	ExpiredUndo     int            `json:"expired_undo"`
	Sensitive       int            `json:"sensitive"`
	ChainHealthy    bool           `json:"chain_healthy"`
	BrokenChains    int            `json:"broken_chains"`
}

// ArchiveReasonValues lists the accepted archive reasons in declaration order.
func ArchiveReasonValues() []string {
	return []string{
		string(ArchiveReasonWrong), string(ArchiveReasonOutdated),
		string(ArchiveReasonDuplicate), string(ArchiveReasonSensitive),
		string(ArchiveReasonSuperseded), string(ArchiveReasonUserRequested),
		string(ArchiveReasonOther),
	}
}

// ArchiveAuthorityValues lists the accepted archive authorities.
func ArchiveAuthorityValues() []string {
	return []string{
		string(ArchiveAuthorityUserRequest),
		string(ArchiveAuthorityAgentVerified),
		string(ArchiveAuthoritySensitive),
	}
}

func normalizeArchiveAuthority(raw string) (ArchiveAuthority, bool) {
	switch ArchiveAuthority(strings.TrimSpace(raw)) {
	case ArchiveAuthorityUserRequest:
		return ArchiveAuthorityUserRequest, true
	case ArchiveAuthorityAgentVerified:
		return ArchiveAuthorityAgentVerified, true
	case ArchiveAuthoritySensitive:
		return ArchiveAuthoritySensitive, true
	default:
		return "", false
	}
}

func normalizeArchiveReason(raw string) (ArchiveReason, bool) {
	switch ArchiveReason(strings.TrimSpace(raw)) {
	case ArchiveReasonWrong:
		return ArchiveReasonWrong, true
	case ArchiveReasonOutdated:
		return ArchiveReasonOutdated, true
	case ArchiveReasonDuplicate:
		return ArchiveReasonDuplicate, true
	case ArchiveReasonSensitive:
		return ArchiveReasonSensitive, true
	case ArchiveReasonSuperseded:
		return ArchiveReasonSuperseded, true
	case ArchiveReasonUserRequested:
		return ArchiveReasonUserRequested, true
	case ArchiveReasonOther:
		return ArchiveReasonOther, true
	default:
		return "", false
	}
}

func normalizeRestoreAuthority(raw string) (ArchiveAuthority, bool) {
	switch ArchiveAuthority(strings.TrimSpace(raw)) {
	case ArchiveAuthorityUserRequest:
		return ArchiveAuthorityUserRequest, true
	case ArchiveAuthorityAgentVerified:
		return ArchiveAuthorityAgentVerified, true
	default:
		return "", false
	}
}

// ParseRestoreAuthority validates an authority that may restore a memory.
func ParseRestoreAuthority(raw string) (ArchiveAuthority, bool) {
	return normalizeRestoreAuthority(raw)
}

// ValidateArchiveInput checks the authority/reason pairing before any write.
func ValidateArchiveInput(authority ArchiveAuthority, reason ArchiveReason, note, duplicateOf string) error {
	auth, ok := normalizeArchiveAuthority(string(authority))
	if !ok {
		return memoryErr(MemoryCodeArchiveAuthority, "authority", "authority must be one of %s", strings.Join(ArchiveAuthorityValues(), ", "))
	}
	why, ok := normalizeArchiveReason(string(reason))
	if !ok {
		return memoryErr(MemoryCodeArchiveAuthority, "reason", "reason must be one of %s", strings.Join(ArchiveReasonValues(), ", "))
	}
	if utf8.RuneCountInString(note) > MemoryArchiveMaxNote {
		return memoryErr(MemoryCodeArchiveNote, "note", "note exceeds %d characters", MemoryArchiveMaxNote)
	}
	if (auth == ArchiveAuthoritySensitive) != (why == ArchiveReasonSensitive) {
		return memoryErr(MemoryCodeArchiveAuthority, "authority", "sensitive authority and sensitive reason must be used together")
	}
	if why == ArchiveReasonOther && strings.TrimSpace(note) == "" {
		return memoryErr(MemoryCodeArchiveNote, "note", "reason other requires a note")
	}
	if why == ArchiveReasonDuplicate && strings.TrimSpace(duplicateOf) == "" {
		return memoryErr(MemoryCodeDuplicateTarget, "duplicate_of_memory_id", "reason duplicate requires a duplicate target")
	}
	if why != ArchiveReasonDuplicate && strings.TrimSpace(duplicateOf) != "" {
		return memoryErr(MemoryCodeDuplicateTarget, "duplicate_of_memory_id", "duplicate target is only valid with reason duplicate")
	}
	return nil
}

// RefuseMemoryHardDelete is the stable refusal for a requested destructive path.
func RefuseMemoryHardDelete() error {
	return memoryErr(MemoryCodeHardDeleteRefused, "", "hard delete is not supported; archive the memory instead")
}

// ArchiveMemory transitions a current memory to archived and appends a
// hash-linked lifecycle event. It never deletes content.
func (s *Store) ArchiveMemory(ctx context.Context, project, memoryID string, authority ArchiveAuthority, reason ArchiveReason, note, actor, duplicateOf string) (*ArchiveRecord, error) {
	return s.archiveMemoryAt(ctx, project, memoryID, authority, reason, note, actor, duplicateOf, time.Now().UTC())
}

func (s *Store) archiveMemoryAt(ctx context.Context, project, memoryID string, authority ArchiveAuthority, reason ArchiveReason, note, actor, duplicateOf string, now time.Time) (*ArchiveRecord, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("memory archive: store is nil")
	}
	if s.ReadOnly {
		return nil, errors.New("memory archive: store is read-only")
	}
	rec, err := s.MemoryRecordByID(memoryID)
	if err != nil {
		return nil, err
	}
	if project != "" && !strings.EqualFold(strings.TrimSpace(project), rec.ProjectID) {
		return nil, memoryErr(MemoryCodeArchiveProject, "project", "memory %q belongs to project %q, not %q", memoryID, rec.ProjectID, project)
	}
	if MemoryState(rec.State) != MemoryStateCurrent {
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "memory %q is %s, not current", memoryID, rec.State)
	}
	if rec.Superseded {
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "memory %q is already superseded by a relation", memoryID)
	}
	return s.archiveMemoryCASAt(ctx, rec, MemoryStateCurrent, authority, reason, note, actor, duplicateOf, now)
}

// archiveMemoryCASAt performs the compare-and-set archive. expected is the
// state observed by the caller; a stale expected value loses with
// MEMORY_STATE_CONFLICT rather than overwriting a concurrent transition.
func (s *Store) archiveMemoryCASAt(ctx context.Context, rec *MemoryRecord, expected MemoryState, authority ArchiveAuthority, reason ArchiveReason, note, actor, duplicateOf string, now time.Time) (*ArchiveRecord, error) {
	if rec == nil {
		return nil, memoryErr(MemoryCodeStateInvalid, "memory_id", "memory record is required")
	}
	auth, _ := normalizeArchiveAuthority(string(authority))
	why, _ := normalizeArchiveReason(string(reason))
	if err := ValidateArchiveInput(auth, why, note, duplicateOf); err != nil {
		return nil, err
	}
	if strings.TrimSpace(actor) == "" {
		if auth == ArchiveAuthorityUserRequest {
			actor = "user"
		} else {
			actor = "agent"
		}
	}
	if expected != MemoryStateCurrent {
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "archive requires a current memory")
	}

	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin memory archive tx: %w", err)
	}
	defer tx.Rollback()

	if duplicateOf != "" {
		if strings.TrimSpace(duplicateOf) == rec.ID {
			return nil, memoryErr(MemoryCodeDuplicateTarget, "duplicate_of_memory_id", "duplicate target cannot be the memory itself")
		}
		var targetProject string
		err := tx.QueryRowContext(ctx, `SELECT project_id FROM memory_records WHERE id = ?`, duplicateOf).Scan(&targetProject)
		if err == sql.ErrNoRows {
			return nil, memoryErr(MemoryCodeDuplicateTarget, "duplicate_of_memory_id", "duplicate target %q does not exist", duplicateOf)
		}
		if err != nil {
			return nil, err
		}
		if targetProject != rec.ProjectID {
			return nil, memoryErr(MemoryCodeDuplicateTarget, "duplicate_of_memory_id", "duplicate target belongs to a different project")
		}
	}

	res, err := tx.ExecContext(ctx, `UPDATE memory_records SET state = ?, updated_at = ? WHERE id = ? AND project_id = ? AND state = ?`,
		string(MemoryStateArchived), now.UTC().Format(time.RFC3339Nano), rec.ID, rec.ProjectID, string(expected))
	if err != nil {
		return nil, fmt.Errorf("archive memory %s: %w", rec.ID, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, memoryErr(MemoryCodeStateConflict, "state", "memory %q changed state while archiving; retry", rec.ID)
	}

	event, err := insertLifecycleEventTx(ctx, tx, rec.ID, rec.ProjectID, MemoryActionArchive,
		string(MemoryStateCurrent), string(MemoryStateArchived), string(auth), string(why), note, actor, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	deadline, agentRestorable := archiveRestorePolicy(string(auth), event.CreatedAt, now)
	return &ArchiveRecord{
		MemoryID:        rec.ID,
		ProjectID:       rec.ProjectID,
		Title:           rec.Title,
		Kind:            rec.Kind,
		State:           string(MemoryStateArchived),
		ArchiveID:       event.EventID,
		Authority:       string(auth),
		Reason:          string(why),
		Note:            note,
		Actor:           actor,
		CreatedAt:       event.CreatedAt,
		RestoreDeadline: deadline,
		AgentRestorable: agentRestorable,
		Restorable:      true,
		Sensitive:       auth == ArchiveAuthoritySensitive,
	}, nil
}

// RestoreMemory returns an archived memory to current. An already-current
// memory is an idempotent no-op with restored=false.
func (s *Store) RestoreMemory(ctx context.Context, project, idOrArchiveID, actor string, authority ArchiveAuthority, now time.Time) (*RestoreResult, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("memory restore: store is nil")
	}
	if s.ReadOnly {
		return nil, errors.New("memory restore: store is read-only")
	}
	auth, ok := normalizeRestoreAuthority(string(authority))
	if !ok {
		return nil, memoryErr(MemoryCodeArchiveAuthority, "authority", "restore authority must be user_request or agent_verified")
	}
	if strings.TrimSpace(actor) == "" {
		if auth == ArchiveAuthorityUserRequest {
			actor = "user"
		} else {
			actor = "agent"
		}
	}
	rec, _, err := s.resolveLifecycleMemory(idOrArchiveID)
	if err != nil {
		return nil, err
	}
	if project != "" && !strings.EqualFold(strings.TrimSpace(project), rec.ProjectID) {
		return nil, memoryErr(MemoryCodeArchiveProject, "project", "memory %q belongs to project %q, not %q", rec.ID, rec.ProjectID, project)
	}

	state := MemoryState(rec.State)
	if state == MemoryStateCurrent && !rec.Superseded {
		return &RestoreResult{MemoryID: rec.ID, Restored: false, State: string(MemoryStateCurrent), Authority: string(auth)}, nil
	}
	if state == MemoryStateSuperseded || rec.Superseded {
		return nil, memoryErr(MemoryCodeSupersededRestore, "state", "memory %q is superseded and cannot be restored", rec.ID)
	}
	if state != MemoryStateArchived {
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "memory %q is %s and cannot be restored", rec.ID, rec.State)
	}

	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin memory restore tx: %w", err)
	}
	defer tx.Rollback()

	// Re-read state and the latest archive event inside the transaction so a
	// concurrent restore+rearchive cannot downgrade a sensitive archive into an
	// agent-restorable one between the caller's first read and this write.
	var currentState string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM memory_records WHERE id = ? AND project_id = ?`,
		rec.ID, rec.ProjectID).Scan(&currentState); err != nil {
		if err == sql.ErrNoRows {
			return nil, memoryErr(MemoryCodeBound, "memory_id", "memory %q not found", rec.ID)
		}
		return nil, err
	}
	switch MemoryState(currentState) {
	case MemoryStateCurrent:
		return &RestoreResult{MemoryID: rec.ID, Restored: false, State: string(MemoryStateCurrent), Authority: string(auth)}, nil
	case MemoryStateSuperseded:
		return nil, memoryErr(MemoryCodeSupersededRestore, "state", "memory %q is superseded and cannot be restored", rec.ID)
	case MemoryStateArchived:
	default:
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "memory %q is %s and cannot be restored", rec.ID, currentState)
	}
	archiveEvent, err := latestArchiveEventTx(ctx, tx, rec.ID)
	if err != nil {
		return nil, err
	}
	if archiveEvent == nil {
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "memory %q is archived without an archive event", rec.ID)
	}
	archiveAuthority := ArchiveAuthority(archiveEvent.Authority)
	deadline, _ := archiveRestorePolicy(archiveEvent.Authority, archiveEvent.CreatedAt, now)
	if archiveAuthority == ArchiveAuthoritySensitive && auth != ArchiveAuthorityUserRequest {
		return nil, memoryErr(MemoryCodeSensitiveRestore, "authority", "sensitive archives can only be restored by the user")
	}
	if archiveAuthority == ArchiveAuthorityAgentVerified && auth == ArchiveAuthorityAgentVerified {
		deadlineTime, parseErr := time.Parse(time.RFC3339Nano, deadline)
		if parseErr != nil || now.After(deadlineTime) {
			return nil, memoryErr(MemoryCodeRestoreWindow, "deadline", "agent restore window closed at %s", deadline)
		}
	}

	res, err := tx.ExecContext(ctx, `UPDATE memory_records SET state = ?, updated_at = ? WHERE id = ? AND project_id = ? AND state = ?`,
		string(MemoryStateCurrent), now.UTC().Format(time.RFC3339Nano), rec.ID, rec.ProjectID, string(MemoryStateArchived))
	if err != nil {
		return nil, fmt.Errorf("restore memory %s: %w", rec.ID, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, memoryErr(MemoryCodeStateConflict, "state", "memory %q changed state while restoring; retry", rec.ID)
	}
	if _, err := insertLifecycleEventTx(ctx, tx, rec.ID, rec.ProjectID, MemoryActionRestore,
		string(MemoryStateArchived), string(MemoryStateCurrent), string(auth), string(auth), "", actor, now); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RestoreResult{
		MemoryID:         rec.ID,
		Restored:         true,
		State:            string(MemoryStateCurrent),
		Authority:        string(auth),
		ArchiveAuthority: archiveEvent.Authority,
	}, nil
}

// SupersedeMemory marks oldID superseded by newID. The relation is validated
// from new to old, both records must be current and in the same project, and
// the old record is never deleted or rewritten.
func (s *Store) SupersedeMemory(ctx context.Context, project, oldID, newID, actor string) (*ArchiveRecord, error) {
	if s == nil || s.DB == nil {
		return nil, errors.New("memory supersede: store is nil")
	}
	if s.ReadOnly {
		return nil, errors.New("memory supersede: store is read-only")
	}
	if strings.TrimSpace(oldID) == "" || strings.TrimSpace(newID) == "" {
		return nil, memoryErr(MemoryCodeStateInvalid, "memory_id", "both old and new memory ids are required")
	}
	if oldID == newID {
		return nil, memoryErr(MemoryCodeStateInvalid, "memory_id", "a memory cannot supersede itself")
	}
	oldRec, err := s.MemoryRecordByID(oldID)
	if err != nil {
		return nil, err
	}
	newRec, err := s.MemoryRecordByID(newID)
	if err != nil {
		return nil, err
	}
	if oldRec.ProjectID != newRec.ProjectID {
		return nil, memoryErr(MemoryCodeArchiveProject, "project", "old and new memories belong to different projects")
	}
	if project != "" && !strings.EqualFold(strings.TrimSpace(project), oldRec.ProjectID) {
		return nil, memoryErr(MemoryCodeArchiveProject, "project", "memory %q belongs to project %q, not %q", oldID, oldRec.ProjectID, project)
	}
	if MemoryState(oldRec.State) != MemoryStateCurrent || MemoryState(newRec.State) != MemoryStateCurrent {
		return nil, memoryErr(MemoryCodeStateInvalid, "state", "both memories must be current to supersede")
	}
	if strings.TrimSpace(actor) == "" {
		actor = "agent"
	}
	now := time.Now().UTC()

	factMu.Lock()
	defer factMu.Unlock()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin memory supersede tx: %w", err)
	}
	defer tx.Rollback()

	var relation int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_relations WHERE memory_id = ? AND relation_type = ? AND target_memory_id = ?`,
		newID, MemoryRelationSupersedes, oldID).Scan(&relation); err != nil {
		return nil, err
	}
	if relation == 0 {
		return nil, memoryErr(MemoryCodeRelationTarget, "relations", "new memory %q has no supersedes relation to %q", newID, oldID)
	}
	if err := supersedesChainContainsTx(ctx, tx, oldID, newID); err != nil {
		return nil, err
	}

	res, err := tx.ExecContext(ctx, `UPDATE memory_records SET state = ?, updated_at = ? WHERE id = ? AND project_id = ? AND state = ?`,
		string(MemoryStateSuperseded), now.UTC().Format(time.RFC3339Nano), oldID, oldRec.ProjectID, string(MemoryStateCurrent))
	if err != nil {
		return nil, fmt.Errorf("supersede memory %s: %w", oldID, err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return nil, memoryErr(MemoryCodeStateConflict, "state", "memory %q changed state while superseding; retry", oldID)
	}
	event, err := insertLifecycleEventTx(ctx, tx, oldID, oldRec.ProjectID, MemoryActionSupersede,
		string(MemoryStateCurrent), string(MemoryStateSuperseded), string(ArchiveAuthorityUserRequest), string(ArchiveReasonSuperseded), "", actor, now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ArchiveRecord{
		MemoryID:   oldRec.ID,
		ProjectID:  oldRec.ProjectID,
		Title:      oldRec.Title,
		Kind:       oldRec.Kind,
		State:      string(MemoryStateSuperseded),
		ArchiveID:  event.EventID,
		Authority:  string(ArchiveAuthorityUserRequest),
		Reason:     string(ArchiveReasonSuperseded),
		Actor:      actor,
		CreatedAt:  event.CreatedAt,
		Restorable: false,
	}, nil
}

func supersedesChainContainsTx(ctx context.Context, tx *sql.Tx, startID, targetID string) error {
	seen := map[string]bool{}
	cursor := startID
	for cursor != "" {
		if cursor == targetID {
			return memoryErr(MemoryCodeRelationCycle, "relations", "supersedes relation forms a cycle")
		}
		if seen[cursor] {
			return nil
		}
		seen[cursor] = true
		var next string
		err := tx.QueryRowContext(ctx, `SELECT target_memory_id FROM memory_relations WHERE memory_id = ? AND relation_type = ?`,
			cursor, MemoryRelationSupersedes).Scan(&next)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		cursor = next
	}
	return nil
}

func (s *Store) resolveLifecycleMemory(idOrArchiveID string) (*MemoryRecord, *MemoryLifecycleEvent, error) {
	id := strings.TrimSpace(idOrArchiveID)
	if id == "" {
		return nil, nil, memoryErr(MemoryCodeStateInvalid, "memory_id", "memory id or archive id is required")
	}
	rec, err := s.MemoryRecordByID(id)
	if err == nil {
		return rec, nil, nil
	}
	if MemoryCode(err) != MemoryCodeBound {
		return nil, nil, err
	}
	event, eventErr := s.lifecycleEventByID(id)
	if eventErr != nil {
		return nil, nil, eventErr
	}
	if event == nil {
		return nil, nil, err
	}
	rec, err = s.MemoryRecordByID(event.MemoryID)
	if err != nil {
		return nil, nil, err
	}
	return rec, event, nil
}

// ListArchivedMemories returns archived and superseded records with their most
// recent archive audit event. Superseded records created through relations
// before this lifecycle still appear.
func (s *Store) ListArchivedMemories(project string) ([]ArchiveRecord, error) {
	return s.ListArchivedMemoriesAt(project, time.Now().UTC())
}

// ListArchivedMemoriesAt is ListArchivedMemories with a fixed clock.
func (s *Store) ListArchivedMemoriesAt(project string, now time.Time) ([]ArchiveRecord, error) {
	query := `SELECT r.id, r.project_id, r.title, r.kind, r.state,
		COALESCE(e.event_id, ''), COALESCE(e.authority, ''), COALESCE(e.reason, ''),
		COALESCE(e.note, ''), COALESCE(e.actor, ''), COALESCE(e.created_at, '')
		FROM memory_records r
		LEFT JOIN memory_lifecycle e ON e.event_id = (
			SELECT event_id FROM memory_lifecycle
			WHERE memory_id = r.id AND to_state = r.state
			ORDER BY created_at DESC, event_id DESC LIMIT 1
		)
		WHERE r.state IN ('archived', 'superseded')
		   OR EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes')`
	var args []any
	if strings.TrimSpace(project) != "" {
		query += ` AND r.project_id = ?`
		args = append(args, project)
	}
	query += ` ORDER BY r.updated_at DESC, r.id DESC`
	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ArchiveRecord
	for rows.Next() {
		var rec ArchiveRecord
		var state, authority, reason string
		if err := rows.Scan(&rec.MemoryID, &rec.ProjectID, &rec.Title, &rec.Kind, &state,
			&rec.ArchiveID, &authority, &reason, &rec.Note, &rec.Actor, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if state == string(MemoryStateCurrent) {
			// Only relation-superseded rows can enter this list while still
			// carrying the legacy current state.
			state = string(MemoryStateSuperseded)
			if reason == "" {
				reason = string(ArchiveReasonSuperseded)
			}
		}
		rec.State = state
		rec.Authority = authority
		rec.Reason = reason
		rec.Sensitive = authority == string(ArchiveAuthoritySensitive)
		if state == string(MemoryStateSuperseded) && rec.Reason == "" {
			rec.Reason = string(ArchiveReasonSuperseded)
		}
		rec.Restorable = state == string(MemoryStateArchived)
		if rec.Restorable {
			rec.RestoreDeadline, rec.AgentRestorable = archiveRestorePolicy(authority, rec.CreatedAt, now)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ComputeArchiveStats aggregates the list output and verifies every chain.
func (s *Store) ComputeArchiveStats(records []ArchiveRecord, now time.Time) ArchiveStats {
	stats := ArchiveStats{ByReason: map[string]int{}, ByAuthority: map[string]int{}, ChainHealthy: true}
	seen := map[string]bool{}
	for _, rec := range records {
		switch rec.State {
		case string(MemoryStateArchived):
			stats.Archived++
			stats.Restorable++
			if rec.AgentRestorable {
				stats.AgentRestorable++
			} else if rec.Authority == string(ArchiveAuthorityAgentVerified) {
				stats.ExpiredUndo++
			}
		case string(MemoryStateSuperseded):
			stats.Superseded++
		}
		if rec.Reason != "" {
			stats.ByReason[rec.Reason]++
		}
		if rec.Authority != "" {
			stats.ByAuthority[rec.Authority]++
		}
		if rec.Sensitive {
			stats.Sensitive++
		}
		if seen[rec.MemoryID] {
			continue
		}
		seen[rec.MemoryID] = true
		if history, err := s.VerifyMemoryHistory(context.Background(), rec.ProjectID, rec.MemoryID); err != nil || !history.Verified {
			stats.ChainHealthy = false
			stats.BrokenChains++
		}
	}
	return stats
}

// MemoryHistoryEvents returns the lifecycle chain for one memory.
func (s *Store) MemoryHistoryEvents(ctx context.Context, project, memoryID string) (*MemoryHistory, error) {
	rec, err := s.MemoryRecordByID(memoryID)
	if err != nil {
		return nil, err
	}
	if project != "" && !strings.EqualFold(strings.TrimSpace(project), rec.ProjectID) {
		return nil, memoryErr(MemoryCodeArchiveProject, "project", "memory %q belongs to project %q, not %q", memoryID, rec.ProjectID, project)
	}
	events, err := s.lifecycleEvents(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	verified, broken := verifyLifecycleChain(events)
	return &MemoryHistory{MemoryID: memoryID, ProjectID: rec.ProjectID, Events: events, Verified: verified, BrokenEventID: broken}, nil
}

// VerifyMemoryHistory walks the chain in created_at, event_id order and
// reports the first broken event with MEMORY_CHAIN_BROKEN.
func (s *Store) VerifyMemoryHistory(ctx context.Context, project, memoryID string) (*MemoryHistory, error) {
	history, err := s.MemoryHistoryEvents(ctx, project, memoryID)
	if err != nil {
		return nil, err
	}
	verified, broken := verifyLifecycleChain(history.Events)
	if !verified {
		history.Verified = false
		history.BrokenEventID = broken
		return history, memoryErr(MemoryCodeChainBroken, "event_hash", "lifecycle chain for %q is broken at %q", memoryID, broken)
	}
	return history, nil
}

func (s *Store) lifecycleEvents(ctx context.Context, memoryID string) ([]MemoryLifecycleEvent, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT event_id, memory_id, project_id, action, from_state, to_state,
		authority, reason, COALESCE(note, ''), actor, created_at, prev_hash, event_hash
		FROM memory_lifecycle WHERE memory_id = ? ORDER BY created_at ASC, event_id ASC`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemoryLifecycleEvent
	for rows.Next() {
		ev, err := scanLifecycleEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *ev)
	}
	return out, rows.Err()
}

func (s *Store) lifecycleEventByID(eventID string) (*MemoryLifecycleEvent, error) {
	row := s.DB.QueryRow(`SELECT event_id, memory_id, project_id, action, from_state, to_state,
		authority, reason, COALESCE(note, ''), actor, created_at, prev_hash, event_hash
		FROM memory_lifecycle WHERE event_id = ?`, eventID)
	ev, err := scanLifecycleEvent(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ev, nil
}

func latestArchiveEventTx(ctx context.Context, tx *sql.Tx, memoryID string) (*MemoryLifecycleEvent, error) {
	row := tx.QueryRowContext(ctx, `SELECT event_id, memory_id, project_id, action, from_state, to_state,
		authority, reason, COALESCE(note, ''), actor, created_at, prev_hash, event_hash
		FROM memory_lifecycle WHERE memory_id = ? AND action = 'archive'
		ORDER BY created_at DESC, event_id DESC LIMIT 1`, memoryID)
	ev, err := scanLifecycleEvent(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ev, nil
}

func scanLifecycleEvent(scanner interface{ Scan(...any) error }) (*MemoryLifecycleEvent, error) {
	var ev MemoryLifecycleEvent
	if err := scanner.Scan(&ev.EventID, &ev.MemoryID, &ev.ProjectID, &ev.Action, &ev.FromState, &ev.ToState,
		&ev.Authority, &ev.Reason, &ev.Note, &ev.Actor, &ev.CreatedAt, &ev.PrevHash, &ev.EventHash); err != nil {
		return nil, err
	}
	return &ev, nil
}

func insertLifecycleEventTx(ctx context.Context, tx *sql.Tx, memoryID, projectID, action, fromState, toState, authority, reason, note, actor string, now time.Time) (*MemoryLifecycleEvent, error) {
	var prevHash string
	err := tx.QueryRowContext(ctx, `SELECT event_hash FROM memory_lifecycle WHERE memory_id = ?
		ORDER BY created_at DESC, event_id DESC LIMIT 1`, memoryID).Scan(&prevHash)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	event := MemoryLifecycleEvent{
		EventID:   "mle_" + NewV7UUID(),
		MemoryID:  memoryID,
		ProjectID: projectID,
		Action:    action,
		FromState: fromState,
		ToState:   toState,
		Authority: authority,
		Reason:    reason,
		Note:      note,
		Actor:     actor,
		CreatedAt: now.UTC().Format(time.RFC3339Nano),
		PrevHash:  prevHash,
	}
	event.EventHash = computeLifecycleEventHash(event)
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_lifecycle(event_id, memory_id, project_id, action, from_state, to_state,
		authority, reason, note, actor, created_at, prev_hash, event_hash)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.EventID, event.MemoryID, event.ProjectID, event.Action, event.FromState, event.ToState,
		event.Authority, event.Reason, memoryNullString(event.Note), event.Actor, event.CreatedAt, event.PrevHash, event.EventHash)
	if err != nil {
		return nil, fmt.Errorf("insert lifecycle event: %w", err)
	}
	return &event, nil
}

type lifecycleHashProjection struct {
	EventID   string `json:"event_id"`
	MemoryID  string `json:"memory_id"`
	ProjectID string `json:"project_id"`
	Action    string `json:"action"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	Authority string `json:"authority"`
	Reason    string `json:"reason"`
	Note      string `json:"note"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
	PrevHash  string `json:"prev_hash"`
}

func computeLifecycleEventHash(event MemoryLifecycleEvent) string {
	payload, _ := json.Marshal(lifecycleHashProjection{
		EventID:   event.EventID,
		MemoryID:  event.MemoryID,
		ProjectID: event.ProjectID,
		Action:    event.Action,
		FromState: event.FromState,
		ToState:   event.ToState,
		Authority: event.Authority,
		Reason:    event.Reason,
		Note:      event.Note,
		Actor:     event.Actor,
		CreatedAt: event.CreatedAt,
		PrevHash:  event.PrevHash,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func archiveRestorePolicy(authority, createdAt string, now time.Time) (deadline string, agentRestorable bool) {
	switch ArchiveAuthority(authority) {
	case ArchiveAuthorityAgentVerified:
		t, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return "", false
		}
		deadlineTime := t.Add(MemoryArchiveUndoWindow)
		return deadlineTime.UTC().Format(time.RFC3339Nano), !now.After(deadlineTime)
	case ArchiveAuthorityUserRequest:
		return "", true
	default:
		return "", false
	}
}

// NonCurrentMemoryFactIDs returns the bridge fact IDs whose typed memory is
// archived, superseded by state, or superseded by a relation. Default read
// paths use it to exclude records from recall, prime and knowledge search.
func (s *Store) NonCurrentMemoryFactIDs() (map[string]bool, error) {
	rows, err := s.DB.Query(`SELECT fact_id FROM memory_records WHERE state != 'current'
		UNION
		SELECT r.fact_id FROM memory_records r
		WHERE EXISTS(SELECT 1 FROM memory_relations x WHERE x.target_memory_id = r.id AND x.relation_type = 'supersedes')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var factID string
		if err := rows.Scan(&factID); err != nil {
			return nil, err
		}
		if factID != "" {
			out[factID] = true
		}
	}
	return out, rows.Err()
}

// FilterNonCurrentFacts removes archived and superseded records from a fact list.
func FilterNonCurrentFacts(facts []Fact, excluded map[string]bool) []Fact {
	if len(excluded) == 0 {
		return facts
	}
	out := make([]Fact, 0, len(facts))
	for _, fact := range facts {
		if excluded[fact.ID] {
			continue
		}
		out = append(out, fact)
	}
	return out
}
