package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestMigration41Additive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v40.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := st.InsertMemo("proj", "Legacy fact survives migration 41", "agent", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.FactByID(fact.ID)
	if err != nil || before == nil {
		t.Fatalf("fact before downgrade: %v", err)
	}
	if _, err := st.DB.Exec(`DROP TABLE IF EXISTS memory_fts;
		DROP TABLE IF EXISTS memory_relations;
		DROP TABLE IF EXISTS memory_evidence;
		DROP TABLE IF EXISTS memory_assertions;
		DROP TABLE IF EXISTS memory_records;
		PRAGMA user_version=40;`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	version, err := st.SchemaVersion()
	if err != nil || version != CurrentSchemaVersion() {
		t.Fatalf("version=%d err=%v", version, err)
	}
	for _, table := range []string{"memory_records", "memory_assertions", "memory_evidence", "memory_relations", "memory_fts"} {
		if !storeTableExists(t, st, table) {
			t.Errorf("missing table %s", table)
		}
	}
	after, err := st.FactByID(fact.ID)
	if err != nil || after == nil {
		t.Fatalf("fact after migration: %v", err)
	}
	if after.FactHash != before.FactHash || after.PrevHash != before.PrevHash || after.Claim != before.Claim {
		t.Fatalf("existing fact changed: before=%+v after=%+v", before, after)
	}
	audit, err := st.VerifyChain("")
	if err != nil || !audit.Intact {
		t.Fatalf("hash chain after migration: %+v err=%v", audit, err)
	}
}

func TestMigration41Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reopen.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", testMemoryEnvelope("proj", "Reopen", "Reopen summary"), Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`PRAGMA user_version=40`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil || loaded.Digest != rec.Digest {
		t.Fatalf("record after reopen: %+v err=%v", loaded, err)
	}
}

func TestMemoryTablesAndIndexes(t *testing.T) {
	st := openMemoryStore(t)
	indexes := []string{
		"idx_memory_project_digest", "idx_memory_idempotency",
		"idx_memory_project_kind", "idx_memory_fact",
		"idx_memory_assertions_predicate", "idx_memory_evidence_kind",
		"idx_memory_relations_target",
	}
	for _, idx := range indexes {
		var name string
		err := st.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type='index' AND name=?`, idx).Scan(&name)
		if err != nil {
			t.Errorf("missing index %s: %v", idx, err)
		}
	}
}

func TestMemoryMigrationRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", testMemoryEnvelope("proj", "Recover", "Recover summary"), Provenance{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`DROP TABLE memory_fts; PRAGMA user_version=40`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatalf("partial table recovery: %v", err)
	}
	defer st.Close()
	if !storeTableExists(t, st, "memory_fts") {
		t.Fatal("FTS table was not recreated")
	}
	if _, err := st.DB.Exec(`UPDATE memory_records SET envelope_json = '{' WHERE id = ?`, rec.ID); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.MemoryRecordByID(rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasMemoryDiagnostic(loaded.Diagnostics, MemoryCodeJSONInvalid) {
		t.Fatalf("corrupt envelope diagnostic missing: %v", loaded.Diagnostics)
	}
	report, err := st.MemoryDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if report.CorruptEnvelopes != 1 {
		t.Fatalf("corrupt envelopes = %d", report.CorruptEnvelopes)
	}
}

func TestMemoryOrphanDiagnostics(t *testing.T) {
	st := openMemoryStore(t)
	if _, _, err := st.SaveMemoryEnvelope(context.Background(), "proj", testMemoryEnvelope("proj", "Orphan", "Orphan summary"), Provenance{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO memory_assertions(memory_id, assertion_id, assertion_type, predicate, statement, subject_json, object_json, confidence, importance)
		VALUES('mem_ghost','A-001','descriptive','causes','ghost','{}','{}',0.5,2);
		INSERT INTO memory_evidence(memory_id, evidence_id, kind, locator_json, summary, sha256, grounded)
		VALUES('mem_ghost','E-001','agent_initiated','{}','ghost',NULL,1);
		INSERT INTO memory_relations(memory_id, relation_type, target_memory_id, evidence_json)
		VALUES('mem_ghost','related_to','mem_missing','[]');
		DELETE FROM memory_fts;`); err != nil {
		t.Fatal(err)
	}
	report, err := st.MemoryDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphanedAssertions != 1 || report.OrphanedEvidence != 1 || report.OrphanedRelations != 1 {
		t.Fatalf("orphan counts = %+v", report)
	}
	if report.BrokenRelationTargets != 1 {
		t.Fatalf("broken targets = %d", report.BrokenRelationTargets)
	}
	if report.FTSMissing != 1 || report.FTSHealthy {
		t.Fatalf("fts health = %+v", report)
	}
	repairs, err := st.RepairMemoryIndexes()
	if err != nil {
		t.Fatal(err)
	}
	if len(repairs) == 0 {
		t.Fatal("repair reported nothing")
	}
	report, err = st.MemoryDiagnostics()
	if err != nil {
		t.Fatal(err)
	}
	if report.OrphanedAssertions != 0 || report.OrphanedEvidence != 0 || report.OrphanedRelations != 0 || report.FTSMissing != 0 {
		t.Fatalf("repair left structural drift: %+v", report)
	}
	if report.Records != 1 {
		t.Fatalf("repair deleted user memory: %+v", report)
	}
}

// --- JSON Schema parity ---

func TestMemorySchemaParity(t *testing.T) {
	schema := loadMemorySchema(t)
	valid := []string{memoryMinimalFixture, memoryFullFixture("sila-cli")}
	for i, fixture := range valid {
		var value any
		if err := json.Unmarshal([]byte(fixture), &value); err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		if errs := validateJSONSchema(schema, schema, value, "$"); len(errs) > 0 {
			t.Fatalf("fixture %d rejected by published schema: %v", i, errs)
		}
	}
	invalid := map[string]func(map[string]any){
		"unknown field":   func(m map[string]any) { m["extra"] = true },
		"bad kind":        func(m map[string]any) { m["kind"] = "guess" },
		"nine assertions": func(m map[string]any) { m["assertions"] = make([]any, 9) },
		"short key":       func(m map[string]any) { m["idempotency_key"] = "short" },
		"absolute path": func(m map[string]any) {
			m["evidence"].([]any)[0].(map[string]any)["kind"] = "file"
			m["evidence"].([]any)[0].(map[string]any)["locator"] = map[string]any{"path": "/etc/passwd"}
		},
		"predicate mismatch": func(m map[string]any) {
			m["assertions"].([]any)[0].(map[string]any)["assertion_type"] = "normative"
			m["assertions"].([]any)[0].(map[string]any)["predicate"] = "causes"
		},
	}
	for name, mutate := range invalid {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal([]byte(memoryMinimalFixture), &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			if errs := validateJSONSchema(schema, schema, value, "$"); len(errs) == 0 {
				t.Fatal("published schema accepted an invalid fixture")
			}
		})
	}
}

func loadMemorySchema(t *testing.T) map[string]any {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "schemas", "sila-memory-v1.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	return schema
}

// validateJSONSchema implements the subset of draft 2020-12 used by the
// published memory schema. It exists so the Go model and the JSON Schema stay
// in lockstep without adding a dependency.
func validateJSONSchema(root, schema map[string]any, value any, path string) []string {
	if schema == nil {
		return nil
	}
	if ref, ok := schema["$ref"].(string); ok {
		return validateJSONSchema(root, resolveLocalRef(root, ref), value, path)
	}
	var errs []string
	appendErrs := func(sub []string) { errs = append(errs, sub...) }

	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			if reflect.DeepEqual(e, value) {
				found = true
				break
			}
		}
		if !found {
			errs = append(errs, fmt.Sprintf("%s: not in enum", path))
		}
	}
	if c, ok := schema["const"]; ok && !reflect.DeepEqual(c, value) {
		errs = append(errs, fmt.Sprintf("%s: does not equal const", path))
	}
	if typ, ok := schema["type"].(string); ok && !jsonTypeMatches(typ, value) {
		return append(errs, fmt.Sprintf("%s: expected %s", path, typ))
	}

	switch v := value.(type) {
	case map[string]any:
		required, _ := schema["required"].([]any)
		for _, req := range required {
			key, _ := req.(string)
			if _, ok := v[key]; !ok {
				errs = append(errs, fmt.Sprintf("%s: missing required %s", path, key))
			}
		}
		props, _ := schema["properties"].(map[string]any)
		for key, item := range v {
			sub, known := props[key]
			if !known {
				if additional, ok := schema["additionalProperties"].(bool); ok && !additional {
					errs = append(errs, fmt.Sprintf("%s: unknown property %s", path, key))
				}
				continue
			}
			subSchema, _ := sub.(map[string]any)
			appendErrs(validateJSONSchema(root, subSchema, item, path+"."+key))
		}
	case []any:
		if min, ok := schema["minItems"].(float64); ok && float64(len(v)) < min {
			errs = append(errs, fmt.Sprintf("%s: fewer than %.0f items", path, min))
		}
		if max, ok := schema["maxItems"].(float64); ok && float64(len(v)) > max {
			errs = append(errs, fmt.Sprintf("%s: more than %.0f items", path, max))
		}
		if unique, _ := schema["uniqueItems"].(bool); unique {
			seen := map[string]bool{}
			for _, item := range v {
				raw, _ := json.Marshal(item)
				if seen[string(raw)] {
					errs = append(errs, fmt.Sprintf("%s: duplicate item", path))
				}
				seen[string(raw)] = true
			}
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range v {
				appendErrs(validateJSONSchema(root, items, item, fmt.Sprintf("%s[%d]", path, i)))
			}
		}
	case string:
		if min, ok := schema["minLength"].(float64); ok && float64(len([]rune(v))) < min {
			errs = append(errs, fmt.Sprintf("%s: shorter than %.0f", path, min))
		}
		if max, ok := schema["maxLength"].(float64); ok && float64(len([]rune(v))) > max {
			errs = append(errs, fmt.Sprintf("%s: longer than %.0f", path, max))
		}
		if pattern, ok := schema["pattern"].(string); ok {
			re, err := regexp.Compile(pattern)
			if err == nil && !re.MatchString(v) {
				errs = append(errs, fmt.Sprintf("%s: does not match pattern", path))
			}
		}
	case float64:
		if min, ok := schema["minimum"].(float64); ok && v < min {
			errs = append(errs, fmt.Sprintf("%s: below minimum", path))
		}
		if max, ok := schema["maximum"].(float64); ok && v > max {
			errs = append(errs, fmt.Sprintf("%s: above maximum", path))
		}
	}

	if oneOf, ok := schema["oneOf"].([]any); ok {
		passes := 0
		for _, sub := range oneOf {
			subSchema, _ := sub.(map[string]any)
			if len(validateJSONSchema(root, subSchema, value, path)) == 0 {
				passes++
			}
		}
		if passes != 1 {
			errs = append(errs, fmt.Sprintf("%s: oneOf matched %d schemas", path, passes))
		}
	}
	if allOf, ok := schema["allOf"].([]any); ok {
		for _, sub := range allOf {
			subSchema, _ := sub.(map[string]any)
			appendErrs(validateJSONSchema(root, subSchema, value, path))
		}
	}
	if anyOf, ok := schema["anyOf"].([]any); ok {
		passes := 0
		for _, sub := range anyOf {
			subSchema, _ := sub.(map[string]any)
			if len(validateJSONSchema(root, subSchema, value, path)) == 0 {
				passes++
			}
		}
		if passes == 0 {
			errs = append(errs, fmt.Sprintf("%s: no anyOf schema matched", path))
		}
	}
	if notSchema, ok := schema["not"].(map[string]any); ok {
		if len(validateJSONSchema(root, notSchema, value, path)) == 0 {
			errs = append(errs, fmt.Sprintf("%s: matched a forbidden schema", path))
		}
	}
	if ifSchema, ok := schema["if"].(map[string]any); ok {
		if len(validateJSONSchema(root, ifSchema, value, path)) == 0 {
			if thenSchema, ok := schema["then"].(map[string]any); ok {
				appendErrs(validateJSONSchema(root, thenSchema, value, path))
			}
		}
	}
	return errs
}

func resolveLocalRef(root map[string]any, ref string) map[string]any {
	if !strings.HasPrefix(ref, "#/") {
		return nil
	}
	var node any = root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = m[part]
	}
	out, _ := node.(map[string]any)
	return out
}

func jsonTypeMatches(typ string, value any) bool {
	switch typ {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		f, ok := value.(float64)
		return ok && math.Trunc(f) == f
	}
	return true
}

func storeTableExists(t *testing.T, st *Store, name string) bool {
	t.Helper()
	var found string
	err := st.DB.QueryRow(`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name=?`, name).Scan(&found)
	return err == nil
}
