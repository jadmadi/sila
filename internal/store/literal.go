package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ByteSpan represents a half-open byte interval [Start, End) within a source text blob.
type ByteSpan struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

// TextChunk represents a byte-bounded segment of a document, message transcript, or source file.
// It stores deterministic byte offsets [ByteStart, ByteEnd) enabling zero-hallucination literal recovery.
type TextChunk struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	SourceType string `json:"source_type"` // "doc", "message", "file"
	SourceID   string `json:"source_id"`   // doc ID, message ID (as string), or relative file path
	ByteStart  int64  `json:"byte_start"`
	ByteEnd    int64  `json:"byte_end"`
	Content    string `json:"content"`
	Hash       string `json:"hash"`
	CreatedAt  string `json:"created_at"`
}

var (
	// ErrInvalidByteBounds is returned when start < 0 or start > end.
	ErrInvalidByteBounds = errors.New("invalid byte bounds: start must be >= 0 and <= end")
	// ErrBoundsOutOfBounds is returned when requested byte bounds exceed data length.
	ErrBoundsOutOfBounds = errors.New("byte bounds exceed blob length")
)

// ExtractByteRange extracts an exact byte slice [start:end] using native Go byte slicing.
// Guarantees zero hallucination quotes by operating directly on raw bytes.
func ExtractByteRange(data []byte, start, end int64) ([]byte, error) {
	if start < 0 || end < start {
		return nil, ErrInvalidByteBounds
	}
	dataLen := int64(len(data))
	if start > dataLen || end > dataLen {
		return nil, fmt.Errorf("%w: requested [%d:%d], length %d", ErrBoundsOutOfBounds, start, end, dataLen)
	}
	out := make([]byte, end-start)
	copy(out, data[start:end])
	return out, nil
}

// ExtractStringRange extracts an exact byte range [start:end] from string s.
func ExtractStringRange(s string, start, end int64) (string, error) {
	raw := []byte(s)
	sliced, err := ExtractByteRange(raw, start, end)
	if err != nil {
		return "", err
	}
	return string(sliced), nil
}

// ComputeChunkID generates a deterministic ID for a chunk based on project, source, and byte offsets.
func ComputeChunkID(projectID, sourceType, sourceID string, start, end int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%d:%d", projectID, sourceType, sourceID, start, end)))
	return "chk_" + hex.EncodeToString(sum[:6])
}

// ChunkBlob deterministically breaks raw data into byte-bounded TextChunks with the given
// chunkSize and overlap (in bytes). Uses native Go byte slicing.
func ChunkBlob(projectID, sourceType, sourceID string, data []byte, chunkSize, overlap int) []TextChunk {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	if overlap < 0 || overlap >= chunkSize {
		overlap = 0
	}
	step := chunkSize - overlap
	dataLen := len(data)
	if dataLen == 0 {
		return nil
	}

	var chunks []TextChunk
	now := time.Now().UTC().Format(time.RFC3339)

	for start := 0; start < dataLen; start += step {
		end := start + chunkSize
		if end > dataLen {
			end = dataLen
		}

		rawSlice := data[start:end]
		h := sha256.Sum256(rawSlice)
		hashStr := hex.EncodeToString(h[:])

		chunk := TextChunk{
			ID:         ComputeChunkID(projectID, sourceType, sourceID, int64(start), int64(end)),
			ProjectID:  projectID,
			SourceType: sourceType,
			SourceID:   sourceID,
			ByteStart:  int64(start),
			ByteEnd:    int64(end),
			Content:    string(rawSlice),
			Hash:       hashStr,
			CreatedAt:  now,
		}
		chunks = append(chunks, chunk)

		if end == dataLen {
			break
		}
	}

	return chunks
}

// InsertTextChunk inserts or updates a byte-bounded text chunk in knowledge.db.
func (s *Store) InsertTextChunk(chunk TextChunk) error {
	if chunk.ID == "" {
		chunk.ID = ComputeChunkID(chunk.ProjectID, chunk.SourceType, chunk.SourceID, chunk.ByteStart, chunk.ByteEnd)
	}
	if chunk.CreatedAt == "" {
		chunk.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if chunk.Hash == "" {
		h := sha256.Sum256([]byte(chunk.Content))
		chunk.Hash = hex.EncodeToString(h[:])
	}

	return WithRetry(context.Background(), func() error {
		_, err := s.DB.Exec(`
			INSERT INTO text_chunks(id, project_id, source_type, source_id, byte_start, byte_end, content, hash, created_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
				byte_start = excluded.byte_start,
				byte_end   = excluded.byte_end,
				content    = excluded.content,
				hash       = excluded.hash;
		`, chunk.ID, chunk.ProjectID, chunk.SourceType, chunk.SourceID, chunk.ByteStart, chunk.ByteEnd, chunk.Content, chunk.Hash, chunk.CreatedAt)
		return err
	})
}

// GetTextChunk retrieves a text chunk by its ID.
func (s *Store) GetTextChunk(id string) (*TextChunk, error) {
	row := s.DB.QueryRow(`
		SELECT id, project_id, source_type, source_id, byte_start, byte_end, content, hash, created_at
		FROM text_chunks WHERE id = ?
	`, id)

	var c TextChunk
	err := row.Scan(&c.ID, &c.ProjectID, &c.SourceType, &c.SourceID, &c.ByteStart, &c.ByteEnd, &c.Content, &c.Hash, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get text chunk %s: %w", id, err)
	}
	return &c, nil
}

// ListTextChunks retrieves all chunks for a source.
func (s *Store) ListTextChunks(projectID, sourceType, sourceID string) ([]TextChunk, error) {
	query := `
		SELECT id, project_id, source_type, source_id, byte_start, byte_end, content, hash, created_at
		FROM text_chunks
		WHERE project_id = ? AND source_type = ? AND source_id = ?
		ORDER BY byte_start ASC
	`
	rows, err := s.DB.Query(query, projectID, sourceType, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list text chunks: %w", err)
	}
	defer rows.Close()

	var chunks []TextChunk
	for rows.Next() {
		var c TextChunk
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.SourceType, &c.SourceID, &c.ByteStart, &c.ByteEnd, &c.Content, &c.Hash, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan text chunk: %w", err)
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// ExtractDocLiteral extracts an exact byte range [start:end] from an attached document blob.
func (s *Store) ExtractDocLiteral(docID string, start, end int64) (string, error) {
	var content string
	err := s.DB.QueryRow(`SELECT content FROM docs WHERE id = ?`, docID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("doc %q not found", docID)
	}
	if err != nil {
		return "", fmt.Errorf("query doc %s: %w", docID, err)
	}
	return ExtractStringRange(content, start, end)
}

// ExtractMessageLiteral extracts an exact byte range [start:end] from a raw message transcript turn.
func (s *Store) ExtractMessageLiteral(messageID int64, start, end int64) (string, error) {
	var content string
	err := s.DB.QueryRow(`SELECT content FROM messages WHERE id = ?`, messageID).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("message %d not found", messageID)
	}
	if err != nil {
		return "", fmt.Errorf("query message %d: %w", messageID, err)
	}
	return ExtractStringRange(content, start, end)
}

// ExtractFileLiteral extracts an exact byte range [start:end] from a file in the workspace.
// Prevents directory traversal attacks by validating relative path containment.
func ExtractFileLiteral(projectRoot, relPath string, start, end int64) (string, error) {
	cleanRel := filepath.Clean(relPath)
	if filepath.IsAbs(cleanRel) || strings.HasPrefix(cleanRel, "..") {
		return "", fmt.Errorf("path %q must be relative to project root", relPath)
	}
	fullPath := filepath.Join(projectRoot, cleanRel)
	resolvedRel, err := filepath.Rel(projectRoot, fullPath)
	if err != nil || strings.HasPrefix(resolvedRel, "..") {
		return "", fmt.Errorf("directory traversal denied for %q", relPath)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		return "", fmt.Errorf("read file %s: %w", relPath, err)
	}

	extracted, err := ExtractByteRange(data, start, end)
	if err != nil {
		return "", err
	}
	return string(extracted), nil
}

// ExtractLiteral recovers the exact literal string given a project, source type, and byte bounds.
func (s *Store) ExtractLiteral(projectRoot, projectID, sourceType, sourceID string, start, end int64) (string, error) {
	switch strings.ToLower(sourceType) {
	case "doc", "docs":
		return s.ExtractDocLiteral(sourceID, start, end)
	case "message", "messages", "msg":
		var msgID int64
		if _, err := fmt.Sscanf(sourceID, "%d", &msgID); err != nil {
			return "", fmt.Errorf("invalid message id %q: must be integer", sourceID)
		}
		return s.ExtractMessageLiteral(msgID, start, end)
	case "file", "code":
		return ExtractFileLiteral(projectRoot, sourceID, start, end)
	default:
		return "", fmt.Errorf("unsupported source type %q (use doc, message, or file)", sourceType)
	}
}

// ExtractFactLiteral extracts the literal source evidence for a fact if byte offsets are tracked.
func (s *Store) ExtractFactLiteral(factID, projectRoot string) (string, error) {
	fact, err := s.FactByID(factID)
	if err != nil {
		return "", fmt.Errorf("query fact %s: %w", factID, err)
	}
	if fact == nil {
		return "", fmt.Errorf("fact %q not found", factID)
	}

	if fact.ByteStart < 0 || fact.ByteEnd <= fact.ByteStart || fact.SourceRef == "" {
		return "", fmt.Errorf("fact %q does not have byte-bounded source offsets (byte_start=%d, byte_end=%d, source_ref=%q)", factID, fact.ByteStart, fact.ByteEnd, fact.SourceRef)
	}

	sourceRef := fact.SourceRef
	if strings.HasPrefix(sourceRef, "doc_") {
		return s.ExtractDocLiteral(sourceRef, fact.ByteStart, fact.ByteEnd)
	}
	if strings.HasPrefix(sourceRef, "msg:") || strings.HasPrefix(sourceRef, "message:") {
		trimmed := strings.TrimPrefix(strings.TrimPrefix(sourceRef, "msg:"), "message:")
		var msgID int64
		if _, err := fmt.Sscanf(trimmed, "%d", &msgID); err == nil {
			return s.ExtractMessageLiteral(msgID, fact.ByteStart, fact.ByteEnd)
		}
	}
	if projectRoot != "" {
		return ExtractFileLiteral(projectRoot, sourceRef, fact.ByteStart, fact.ByteEnd)
	}

	return "", fmt.Errorf("cannot resolve source_ref %q for fact %q without projectRoot", sourceRef, factID)
}
