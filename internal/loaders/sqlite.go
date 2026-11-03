package loaders

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

// openReadOnly opens a source tool DB strictly read-only and enables a 256MB
// mmap for fast scanning of multi-GB stores (e.g. Devin). Never modifies
// another tool's database.
func openReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA mmap_size=268435456"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
