package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/virtuos/keyosk/internal/database"
)

// backupDatabase writes a copy of the database to target. It is consistent
// while the server runs and writes: VACUUM INTO reads the database in one
// transaction, like the sqlite3 shell's .backup, and is plain SQL, so it works
// with whichever driver sqliteshim picked and needs no sqlite3 in the image.
//
// The copy is written to a temporary file next to target and renamed into
// place once complete, so a backup that fails halfway leaves no partial file
// behind, and an existing backup is only ever replaced by a complete one. That
// replacing is deliberate: the image has no shell to delete old backups with,
// so a cron job writes to the same file every time.
func backupDatabase(ctx context.Context, dbPath, target string, out io.Writer) error {
	// Opening a path that does not exist would create an empty database
	// there, and the command would back that up.
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no database at %s; set DB_PATH to the server's database", dbPath)
		}
		return err
	}
	if err := refuseDatabaseFile(dbPath, target); err != nil {
		return err
	}

	// VACUUM INTO accepts an empty file, so the temporary file can be created
	// here, with CreateTemp's 0600: the copy holds everyone's name and email.
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath) // fails harmlessly once renamed

	db, err := database.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", tmpPath); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}

	info, err := os.Stat(target)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Wrote a backup of %s to %s (%d bytes).\n", dbPath, target, info.Size())
	return nil
}

// refuseDatabaseFile turns away a target that is the database itself or one
// of the files SQLite keeps next to it, which the rename would replace.
func refuseDatabaseFile(dbPath, target string) error {
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return fmt.Errorf("%s is a directory; name the backup file", target)
		}
		if dbInfo, err := os.Stat(dbPath); err == nil && os.SameFile(info, dbInfo) {
			return fmt.Errorf("%s is the database itself; write the backup to another file", target)
		}
	}
	absDB, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if absTarget == absDB+suffix {
			return fmt.Errorf("%s belongs to the database; write the backup to another file", target)
		}
	}
	return nil
}
