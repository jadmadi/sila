package ccli

import "testing"

func TestCollisionOrderedOverlapVsAmbiguous(t *testing.T) {
	// 1. Embedded Sila inventory has an ordered overlap (`remember error <symptom>` vs `remember <directive>`)
	// that must be classified as CollisionOrderedOverlap and NOT fail DetectCollisions.
	silaInv := NewSilaInventory()
	ordered, ambiguous := AnalyzeCollisions(silaInv)
	if len(ambiguous) != 0 {
		t.Fatalf("expected 0 ambiguous collisions in SilaInventory, got %v", ambiguous)
	}
	foundRememberOverlap := false
	for _, o := range ordered {
		if o.Type == CollisionOrderedOverlap && o.WinnerProduction == "sila_recall_symptom" {
			foundRememberOverlap = true
			if o.ScoreA.TotalScore == o.ScoreB.TotalScore {
				t.Fatalf("expected distinct specificity scores for ordered overlap, got %+v vs %+v", o.ScoreA, o.ScoreB)
			}
		}
	}
	if !foundRememberOverlap {
		t.Fatalf("expected CollisionOrderedOverlap for sila_recall_symptom vs sila_memo_save, got ordered=%v", ordered)
	}

	// 2. Ambiguous fixtures in BuildCollisionInventory must still fail DetectCollisions.
	for _, id := range []string{
		"collision_exact_skeleton_status_service",
		"collision_keyword_slot_overlap_logs",
		"collision_optional_segment_collapse_deploy",
		"collision_commutative_overlap_copy",
		"collision_keyword_slot_inspect_metrics",
		"collision_optional_collapse_restart",
	} {
		t.Run(id, func(t *testing.T) {
			inv := BuildCollisionInventory(id)
			reports, err := DetectCollisions(inv)
			if err == nil || len(reports) == 0 {
				t.Fatalf("expected ambiguous collision error for %s, got nil", id)
			}
			if !reports[0].Ambiguous {
				t.Fatalf("expected Ambiguous=true on %s report, got %+v", id, reports[0])
			}
		})
	}
}
