package store

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func mergeStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "slot.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestMergeSlotAcquireAndStatus(t *testing.T) {
	st := mergeStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	slot, err := st.AcquireMergeSlot(ctx, "p", "agent-a", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(slot.Token) {
		t.Fatalf("token = %q", slot.Token)
	}
	current, err := st.MergeSlot(ctx, "p")
	if err != nil || current == nil || current.Holder != "agent-a" || !current.Expiry.Equal(now.Add(time.Minute)) {
		t.Fatalf("current = %+v err=%v", current, err)
	}
	if _, err := st.AcquireMergeSlot(ctx, "p", "agent-b", time.Minute, now.Add(30*time.Second)); !errors.Is(err, ErrMergeSlotBusy) {
		t.Fatalf("busy err = %v", err)
	} else {
		var busy *MergeSlotBusyError
		if !errors.As(err, &busy) || busy.Holder != "agent-a" {
			t.Fatalf("busy = %+v", err)
		}
	}
}

func TestMergeSlotExpiryReclaim(t *testing.T) {
	st := mergeStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	if _, err := st.AcquireMergeSlot(ctx, "p", "old", time.Minute, now); err != nil {
		t.Fatal(err)
	}
	// Exactly at expiry counts as expired.
	slot, err := st.AcquireMergeSlot(ctx, "p", "new", time.Minute, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if slot.ReclaimedFrom != "old" {
		t.Fatalf("reclaimed_from = %q", slot.ReclaimedFrom)
	}
}

func TestMergeSlotReleaseAndHeartbeat(t *testing.T) {
	st := mergeStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	slot, err := st.AcquireMergeSlot(ctx, "p", "agent", time.Minute, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ReleaseMergeSlot(ctx, "p", "wrong", false); !errors.Is(err, ErrMergeSlotToken) {
		t.Fatalf("wrong token err = %v", err)
	}
	if current, _ := st.MergeSlot(ctx, "p"); current == nil || current.Holder != "agent" {
		t.Fatal("wrong token release removed the holder")
	}
	extended, err := st.HeartbeatMergeSlot(ctx, "p", slot.Token, 2*time.Minute, now.Add(time.Minute))
	if err != nil || !extended.Expiry.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("heartbeat = %+v err=%v", extended, err)
	}
	if err := st.ReleaseMergeSlot(ctx, "p", slot.Token, false); err != nil {
		t.Fatal(err)
	}
	if current, _ := st.MergeSlot(ctx, "p"); current != nil {
		t.Fatal("release kept the slot")
	}
}

func TestMergeSlotLeaseClampAndValidation(t *testing.T) {
	st := mergeStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	slot, err := st.AcquireMergeSlot(ctx, "p", "agent", 5*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if !slot.Expiry.Equal(now.Add(4 * time.Hour)) {
		t.Fatalf("expiry = %s", slot.Expiry)
	}
	if _, err := st.AcquireMergeSlot(ctx, "q", "", time.Minute, now); err == nil {
		t.Fatal("empty holder accepted")
	}
}
