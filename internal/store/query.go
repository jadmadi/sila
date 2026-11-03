package store

import (
	"context"
	"database/sql"
	"errors"
)

// QueryRows executes a query with args, calling scan for each row and collecting results into a typed slice []T.
func (s *Store) QueryRows[T any](ctx context.Context, query string, scan func(*sql.Rows) (T, error), args ...any) ([]T, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// QueryOne executes a query with args and scans a single row using scan.
// If no row is returned (sql.ErrNoRows), it returns (zero, false, nil).
// If a row is returned, it returns (item, true, nil).
func (s *Store) QueryOne[T any](ctx context.Context, query string, scan func(*sql.Row) (T, error), args ...any) (T, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	row := s.DB.QueryRowContext(ctx, query, args...)
	item, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var zero T
			return zero, false, nil
		}
		var zero T
		return zero, false, err
	}
	return item, true, nil
}
