package store

import "database/sql"

func init() {
	migrations = append(migrations, migration{
		version: 48,
		name:    "agent_session_pid_start_ticks",
		up: func(tx *sql.Tx) error {
			var count int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agent_sessions') WHERE name = 'pid_start_ticks'`).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return nil
			}
			_, err := tx.Exec(`ALTER TABLE agent_sessions ADD COLUMN pid_start_ticks INTEGER NOT NULL DEFAULT 0`)
			return err
		},
	})
}
