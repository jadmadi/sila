package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractByteRange(t *testing.T) {
	data := []byte("Hello, Sila Literal Recovery World!")

	// Valid slice
	out, err := ExtractByteRange(data, 7, 11)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != "Sila" {
		t.Fatalf("expected 'Sila', got %q", string(out))
	}

	// Full slice
	out, err = ExtractByteRange(data, 0, int64(len(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(out) != string(data) {
		t.Fatalf("expected full data, got %q", string(out))
	}

	// Zero length range
	out, err = ExtractByteRange(data, 5, 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty slice, got %q", string(out))
	}

	// Negative start
	if _, err := ExtractByteRange(data, -1, 5); err == nil {
		t.Fatalf("expected error for negative start")
	}

	// Start > end
	if _, err := ExtractByteRange(data, 10, 5); err == nil {
		t.Fatalf("expected error for start > end")
	}

	// End > length
	if _, err := ExtractByteRange(data, 0, int64(len(data))+1); err == nil {
		t.Fatalf("expected error for end out of bounds")
	}
}

func TestExtractStringRange(t *testing.T) {
	s := "Gobstopper byte-bounding pattern"
	extracted, err := ExtractStringRange(s, 0, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if extracted != "Gobstopper" {
		t.Fatalf("expected 'Gobstopper', got %q", extracted)
	}
}

func TestChunkBlob(t *testing.T) {
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	chunks := ChunkBlob("test_proj", "file", "test.txt", data, 10, 2)
	if len(chunks) == 0 {
		t.Fatalf("expected chunks, got 0")
	}

	// Verify first chunk
	if chunks[0].ByteStart != 0 || chunks[0].ByteEnd != 10 {
		t.Fatalf("chunk 0 bounds mismatch: [%d:%d]", chunks[0].ByteStart, chunks[0].ByteEnd)
	}
	if chunks[0].Content != "0123456789" {
		t.Fatalf("chunk 0 content mismatch: %q", chunks[0].Content)
	}

	// Verify overlap: chunk 1 should start at 10 - 2 = 8
	if chunks[1].ByteStart != 8 || chunks[1].ByteEnd != 18 {
		t.Fatalf("chunk 1 bounds mismatch: [%d:%d]", chunks[1].ByteStart, chunks[1].ByteEnd)
	}
	if chunks[1].Content != "89abcdefgh" {
		t.Fatalf("chunk 1 content mismatch: %q", chunks[1].Content)
	}
}

func TestStoreTextChunksAndLiteralRecovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer st.Close()

	// 1. Test TextChunk CRUD
	chunk := TextChunk{
		ProjectID:  "proj_lit",
		SourceType: "doc",
		SourceID:   "doc_spec_1",
		ByteStart:  0,
		ByteEnd:    15,
		Content:    "Architecture Doc",
	}
	if err := st.InsertTextChunk(chunk); err != nil {
		t.Fatalf("InsertTextChunk failed: %v", err)
	}

	chunkID := ComputeChunkID("proj_lit", "doc", "doc_spec_1", 0, 15)
	retrieved, err := st.GetTextChunk(chunkID)
	if err != nil {
		t.Fatalf("GetTextChunk failed: %v", err)
	}
	if retrieved == nil || retrieved.Content != "Architecture Doc" {
		t.Fatalf("unexpected chunk content: %+v", retrieved)
	}

	chunks, err := st.ListTextChunks("proj_lit", "doc", "doc_spec_1")
	if err != nil || len(chunks) != 1 {
		t.Fatalf("ListTextChunks failed: err=%v, count=%d", err, len(chunks))
	}

	// 2. Test ExtractDocLiteral
	doc := Doc{
		ID:        "doc_test_1",
		ProjectID: "proj_lit",
		Title:     "System Design",
		Content:   "Section 1: Byte-bounded literal recovery ensures 100% precision.",
	}
	if err := st.InsertDoc(doc); err != nil {
		t.Fatalf("InsertDoc failed: %v", err)
	}

	literal, err := st.ExtractDocLiteral(doc.ID, 11, 40)
	if err != nil {
		t.Fatalf("ExtractDocLiteral failed: %v", err)
	}
	if literal != "Byte-bounded literal recovery" {
		t.Fatalf("expected 'Byte-bounded literal recovery', got %q", literal)
	}

	// 3. Test ExtractMessageLiteral
	msgContent := "The database schema uses modernc.org/sqlite with WAL mode."
	msgHash := ContentHash("assistant", msgContent)
	res, err := st.DB.Exec(`
		INSERT INTO messages (project_id, content_hash, ts, role, content, source_tools, first_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "proj_lit", msgHash, "2026-10-01T00:00:00Z", "assistant", msgContent, `["antigravity"]`, "2026-10-01T00:00:00Z")
	if err != nil {
		t.Fatalf("Insert message failed: %v", err)
	}
	msgID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId failed: %v", err)
	}

	msgLiteral, err := st.ExtractMessageLiteral(msgID, 25, 43)
	if err != nil {
		t.Fatalf("ExtractMessageLiteral failed: %v", err)
	}
	if msgLiteral != "modernc.org/sqlite" {
		t.Fatalf("expected 'modernc.org/sqlite', got %q", msgLiteral)
	}

	// 4. Test ExtractFileLiteral and Directory Traversal Prevention
	projectRoot := dir
	relFile := "sample.txt"
	absFile := filepath.Join(projectRoot, relFile)
	if err := os.WriteFile(absFile, []byte("Strict reference fidelity without hallucination."), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	fileLiteral, err := ExtractFileLiteral(projectRoot, relFile, 7, 25)
	if err != nil {
		t.Fatalf("ExtractFileLiteral failed: %v", err)
	}
	if fileLiteral != "reference fidelity" {
		t.Fatalf("expected 'reference fidelity', got %q", fileLiteral)
	}

	// Test traversal rejection
	if _, err := ExtractFileLiteral(projectRoot, "../secret.txt", 0, 5); err == nil {
		t.Fatalf("expected directory traversal rejection for ../secret.txt")
	}

	// 5. Test Unified ExtractLiteral
	unified, err := st.ExtractLiteral(projectRoot, "proj_lit", "doc", doc.ID, 0, 9)
	if err != nil || unified != "Section 1" {
		t.Fatalf("ExtractLiteral doc failed: %v, got %q", err, unified)
	}

	// 6. Test Fact with ByteStart/ByteEnd and ExtractFactLiteral
	fact := Fact{
		ID:         "fact_lit_1",
		ProjectID:  "proj_lit",
		Claim:      "Doc references byte-bounded literal recovery",
		ByteStart:  11,
		ByteEnd:    40,
		SourceRef:  doc.ID,
		Confidence: "grounded",
	}
	if err := st.UpsertFact(fact); err != nil {
		t.Fatalf("UpsertFact failed: %v", err)
	}

	factLit, err := st.ExtractFactLiteral(fact.ID, projectRoot)
	if err != nil {
		t.Fatalf("ExtractFactLiteral failed: %v", err)
	}
	if factLit != "Byte-bounded literal recovery" {
		t.Fatalf("expected 'Byte-bounded literal recovery', got %q", factLit)
	}
}
