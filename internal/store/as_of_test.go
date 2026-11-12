package store

import (
	"errors"
	"testing"
	"time"
)

func TestParseAsOfFormats(t *testing.T) {
	now := time.Date(2029, 6, 1, 12, 0, 0, 0, time.UTC)
	date, clamped, err := ParseAsOf("2026-02-28", now)
	if err != nil || clamped || !date.Equal(time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("date = %s clamped=%v err=%v", date, clamped, err)
	}
	offset, clamped, err := ParseAsOf("2026-03-01T10:30:00+03:00", now)
	if err != nil || clamped || offset.Format(time.RFC3339) != "2026-03-01T10:30:00+03:00" {
		t.Fatalf("offset = %s err=%v", offset.Format(time.RFC3339), err)
	}
	if _, _, err := ParseAsOf("not-a-date", now); !errors.Is(err, ErrAsOfFormat) {
		t.Fatalf("garbage err = %v", err)
	}
	if _, _, err := ParseAsOf("2026-02-30", now); !errors.Is(err, ErrAsOfFormat) {
		t.Fatalf("invalid day err = %v", err)
	}
	future, clamped, err := ParseAsOf("2030-01-01", now)
	if err != nil || !clamped || !future.Equal(now) {
		t.Fatalf("future = %s clamped=%v err=%v", future, clamped, err)
	}
	leap, _, err := ParseAsOf("2028-02-29", now)
	if err != nil || leap.Day() != 29 {
		t.Fatalf("leap = %s err=%v", leap, err)
	}
}

func TestFactVisibleAt(t *testing.T) {
	asOf := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name                                     string
		created, superseded, archived, compacted string
		visible, legacy                          bool
	}{
		{"created before", "2026-01-01T00:00:00Z", "", "", "", true, false},
		{"created after", "2026-07-01T00:00:00Z", "", "", "", false, false},
		{"superseded before", "2026-01-01T00:00:00Z", "2026-05-01T00:00:00Z", "", "", false, false},
		{"superseded after", "2026-01-01T00:00:00Z", "2026-07-01T00:00:00Z", "", "", true, false},
		{"archived before", "2026-01-01T00:00:00Z", "", "2026-05-01T00:00:00Z", "", false, false},
		{"compacted after", "2026-01-01T00:00:00Z", "", "", "2026-07-01T00:00:00Z", true, false},
		{"empty created", "", "", "", "", true, true},
	}
	for _, tc := range cases {
		visible, legacy := FactVisibleAt(tc.created, tc.superseded, tc.archived, tc.compacted, asOf)
		if visible != tc.visible || legacy != tc.legacy {
			t.Fatalf("%s = visible %v legacy %v", tc.name, visible, legacy)
		}
	}
}
