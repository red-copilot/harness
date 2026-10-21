package sqlite

import "database/sql"

// DB exposes the underlying handle for migration/diagnostic tests.
func (s *Store) DB() *sql.DB { return s.db }
