package main

import (
	"fmt"
	"os"
)

// requireDatabase fails unless the server's database exists at dbPath. The
// commands that open it check first: opening a path that does not exist would
// create an empty database there, and a mistyped DB_PATH would look like a
// database without keys or accounts.
func requireDatabase(dbPath string) error {
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no database at %s; set DB_PATH to the server's database", dbPath)
		}
		return err
	}
	return nil
}
