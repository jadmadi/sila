package store

import "database/sql"

func init() {
	migrations = append(migrations, migration{
		version: 51,
		name:    "messages_outcome",
		up: func(tx *sql.Tx) error {
			var columns int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = 'outcome'`).Scan(&columns); err != nil {
				return err
			}
			if columns == 0 {
				if _, err := tx.Exec(`ALTER TABLE messages ADD COLUMN outcome TEXT NOT NULL DEFAULT 'neutral'`); err != nil {
					return err
				}
			}
			_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_messages_outcome ON messages(project_id, outcome)`)
			return err
		},
	})
}
