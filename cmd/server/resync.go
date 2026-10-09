package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/virtuos/ai-self-service/internal/database"
)

// resyncLimits marks every key as out of date, so the running server's limit
// sync pushes each key's limits again. It is for undoing changes made directly
// in the gateway, which the portal cannot see; run it nightly from cron to
// keep the two in line.
//
// It pushes nothing itself. The server does that, at the pace
// LIMIT_SYNC_WORKERS allows and with progress in the admin panel, so the
// gateway is never hit from two places at once.
func resyncLimits(ctx context.Context, dbPath string, out io.Writer) error {
	// Opening a path that does not exist would create an empty database
	// there, and the command would cheerfully report zero keys.
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no database at %s; set DB_PATH to the server's database", dbPath)
		}
		return err
	}

	db, err := database.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	n, err := database.NewStore(db).MarkAllLimitsPending(ctx)
	if err != nil {
		return fmt.Errorf("mark keys for a resync (has this version of the server run once, to migrate the database?): %w", err)
	}
	keys := "keys"
	if n == 1 {
		keys = "key"
	}
	fmt.Fprintf(out, "Marked %d %s for a limit resync. The running server pushes the limits within LIMIT_SYNC_INTERVAL; the admin panel shows the progress.\n", n, keys)
	return nil
}
