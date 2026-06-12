// Package repo centralizes all SQLite entity data-access objects (DAOs) and model structs.
//
// Design conventions (see AGENTS.md §4 / §13):
//   - Each table maps to one file containing the model struct + XxxRepo + scan helper.
//   - Business packages depend only on the Repo types and models exposed here; never assemble SQL directly.
//   - This package depends only on the standard library and database/sql; it never reverse-imports
//     any business package, avoiding circular dependencies (business packages use type aliases to reuse models).
package repo

// boolInt converts a bool to 0/1 for SQLite storage.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullStr converts an empty string to SQL NULL; non-empty strings are returned as-is.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
