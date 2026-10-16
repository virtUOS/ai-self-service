package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/virtuos/keyosk/internal/database"
)

// makeAdmin gives an account the admin panel. It is how the first admin is
// marked when the panel manages admins, and the way back in if every admin is
// ever lost. The account settings page shows the ID; the person has to have
// logged in once, so that the account exists.
//
// It refuses when ADMIN_GROUP is set: the login system then decides at every
// login, and would take the flag away again at the person's next one.
func makeAdmin(ctx context.Context, dbPath string, adminGroup bool, userID int64, out io.Writer) error {
	if adminGroup {
		return errors.New("ADMIN_GROUP is set, so admins are the members of that group in the login system; add the person there")
	}
	if userID <= 0 {
		return fmt.Errorf("%d is not an account ID; the ID is shown on the person's account settings page", userID)
	}
	if err := requireDatabase(dbPath); err != nil {
		return err
	}

	db, err := database.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	store := database.NewStore(db)

	// Loaded first, so the output names the person and a mistyped ID shows.
	user, err := store.GetUserByID(ctx, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("no account with ID %d; the person has to log in once first, and the ID is shown on their account settings page", userID)
	}
	if err != nil {
		return err
	}
	if user.IsAdmin {
		fmt.Fprintf(out, "%s is already an admin.\n", describeUser(user))
		return nil
	}
	if err := store.SetUserAdmin(ctx, user.ID, true); err != nil {
		return err
	}
	// As in the panel, losing the audit row must not undo the promotion.
	if err := store.RecordAudit(ctx, &database.AuditEvent{
		Action:       database.AuditAdminGranted,
		ActorEmail:   "command line", // whoever runs the server, not a portal user
		SubjectEmail: user.Email,
		SubjectID:    &user.ID,
		Detail:       "admin granted",
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not record the audit event: %v\n", err)
	}
	fmt.Fprintf(out, "%s is now an admin. It takes effect on their next request.\n", describeUser(user))
	return nil
}

func describeUser(u *database.User) string {
	return fmt.Sprintf("Account %d (%s, %s)", u.ID, u.Name, u.Email)
}
