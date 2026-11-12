package store

import "database/sql"

func init() {
	migrations = append(migrations, migration{
		version: 52,
		name:    "facts_as_of_index",
		up: func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_facts_project_first_seen ON facts(project_id, first_seen)`)
			return err
		},
	})
}
