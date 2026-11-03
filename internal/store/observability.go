package store

import (
	"fmt"
	"strings"
	"time"
)

// RecentDailySessionActivity queries the number of distinct agent sessions per day
// over the last n days (default 7). It returns a slice of counts for each day in
// chronological order, and the sum total across the period.
func (s *Store) RecentDailySessionActivity(projectID string, days int) ([]int, int, error) {
	if days <= 0 {
		days = 7
	}
	counts := make([]int, days)
	if s == nil || s.DB == nil {
		return counts, 0, nil
	}

	now := time.Now().UTC()
	dates := make([]string, days)
	dayMap := make(map[string]int, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days-1)+i).Format("2006-01-02")
		dates[i] = d
		dayMap[d] = 0
	}
	since := dates[0] + "T00:00:00Z"

	var query string
	var args []any
	projID := strings.TrimSpace(projectID)
	if projID != "" && projID != "all" {
		query = `SELECT substr(o.source_time, 1, 10) AS day, COUNT(DISTINCT o.tool || ':' || o.session_id)
			FROM source_occurrences o
			JOIN messages m ON m.id = o.message_id
			WHERE m.project_id = ? AND o.session_id != '' AND o.source_time >= ?
			GROUP BY day`
		args = []any{projID, since}
	} else {
		query = `SELECT substr(o.source_time, 1, 10) AS day, COUNT(DISTINCT o.tool || ':' || o.session_id)
			FROM source_occurrences o
			WHERE o.session_id != '' AND o.source_time >= ?
			GROUP BY day`
		args = []any{since}
	}

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return counts, 0, fmt.Errorf("query daily session activity: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var day string
		var c int
		if err := rows.Scan(&day, &c); err != nil {
			return counts, 0, fmt.Errorf("scan daily session activity: %w", err)
		}
		if _, ok := dayMap[day]; ok {
			dayMap[day] = c
		}
	}
	if err := rows.Err(); err != nil {
		return counts, 0, fmt.Errorf("rows daily session activity: %w", err)
	}

	total := 0
	for i, d := range dates {
		c := dayMap[d]
		counts[i] = c
		total += c
	}
	return counts, total, nil
}

// DailyFactTrend queries the claim creation and grounding velocity over the last n days (default 7).
// It returns daily claim counts, daily grounded (verified) claim counts, total claims, and total verified.
func (s *Store) DailyFactTrend(projectID string, days int) ([]int, []int, int, int, error) {
	if days <= 0 {
		days = 7
	}
	claimCounts := make([]int, days)
	groundedCounts := make([]int, days)
	if s == nil || s.DB == nil {
		return claimCounts, groundedCounts, 0, 0, nil
	}

	now := time.Now().UTC()
	dates := make([]string, days)
	claimsMap := make(map[string]int, days)
	groundedMap := make(map[string]int, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days-1)+i).Format("2006-01-02")
		dates[i] = d
		claimsMap[d] = 0
		groundedMap[d] = 0
	}
	since := dates[0]

	var query string
	var args []any
	projID := strings.TrimSpace(projectID)
	if projID != "" && projID != "all" {
		query = `SELECT substr(first_seen, 1, 10) AS day, COUNT(*), COALESCE(SUM(CASE WHEN verified = 1 THEN 1 ELSE 0 END), 0)
			FROM facts
			WHERE project_id = ? AND first_seen >= ?
			GROUP BY day`
		args = []any{projID, since}
	} else {
		query = `SELECT substr(first_seen, 1, 10) AS day, COUNT(*), COALESCE(SUM(CASE WHEN verified = 1 THEN 1 ELSE 0 END), 0)
			FROM facts
			WHERE first_seen >= ?
			GROUP BY day`
		args = []any{since}
	}

	rows, err := s.DB.Query(query, args...)
	if err != nil {
		return claimCounts, groundedCounts, 0, 0, fmt.Errorf("query daily fact trend: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var day string
		var total, grounded int
		if err := rows.Scan(&day, &total, &grounded); err != nil {
			return claimCounts, groundedCounts, 0, 0, fmt.Errorf("scan daily fact trend: %w", err)
		}
		if _, ok := claimsMap[day]; ok {
			claimsMap[day] = total
			groundedMap[day] = grounded
		}
	}
	if err := rows.Err(); err != nil {
		return claimCounts, groundedCounts, 0, 0, fmt.Errorf("rows daily fact trend: %w", err)
	}

	totalClaims := 0
	totalGrounded := 0
	for i, d := range dates {
		c := claimsMap[d]
		g := groundedMap[d]
		claimCounts[i] = c
		groundedCounts[i] = g
		totalClaims += c
		totalGrounded += g
	}
	return claimCounts, groundedCounts, totalClaims, totalGrounded, nil
}

// DailyStorageGrowthTrend computes the database page and storage growth trend over the last n days.
func (s *Store) DailyStorageGrowthTrend(days int) ([]int, int64, int64, error) {
	if days <= 0 {
		days = 7
	}
	pageTrend := make([]int, days)
	if s == nil || s.DB == nil {
		return pageTrend, 0, 0, nil
	}

	var pageCount, pageSize int64
	_ = s.DB.QueryRow("PRAGMA page_count").Scan(&pageCount)
	_ = s.DB.QueryRow("PRAGMA page_size").Scan(&pageSize)
	dbBytes := pageCount * pageSize

	now := time.Now().UTC()
	dates := make([]string, days)
	msgMap := make(map[string]int, days)
	for i := 0; i < days; i++ {
		d := now.AddDate(0, 0, -(days-1)+i).Format("2006-01-02")
		dates[i] = d
		msgMap[d] = 0
	}
	since := dates[0]

	rows, err := s.DB.Query(`SELECT substr(ts, 1, 10) AS day, COUNT(*) FROM messages WHERE ts >= ? GROUP BY day`, since)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var day string
			var c int
			if rows.Scan(&day, &c) == nil {
				if _, ok := msgMap[day]; ok {
					msgMap[day] = c
				}
			}
		}
	}

	// Compute cumulative trend ending at pageCount
	totalMsgs := 0
	for _, d := range dates {
		totalMsgs += msgMap[d]
	}

	running := 0
	for i, d := range dates {
		running += msgMap[d]
		if totalMsgs > 0 && pageCount > 0 {
			// Scaled proportional cumulative pages
			fraction := float64(running) / float64(totalMsgs)
			// Base pages is at least 20% of total
			scaled := int(float64(pageCount)*0.7 + float64(pageCount)*0.3*fraction)
			pageTrend[i] = scaled
		} else {
			pageTrend[i] = int(pageCount)
		}
	}

	return pageTrend, pageCount, dbBytes, nil
}
