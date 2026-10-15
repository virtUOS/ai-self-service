package handlers

import "github.com/virtuos/keyosk/internal/database"

// adminRow is one line of the Admins tab.
type adminRow struct {
	database.User
	// IsSelf marks the acting admin's own row, which offers no remove button:
	// an admin cannot remove their own rights, so the panel can never be left
	// with nobody in charge by accident.
	IsSelf    bool
	Removable bool
}

// adminRows lists every account that holds the panel, marking the acting
// admin's own row as not removable.
func adminRows(admins []database.User, actorID int64) []adminRow {
	rows := make([]adminRow, 0, len(admins))
	for _, u := range admins {
		self := u.ID == actorID
		rows = append(rows, adminRow{User: u, IsSelf: self, Removable: !self})
	}
	return rows
}

// adminCandidates are the accounts the promotion form offers: everyone the
// portal has seen who is not an admin yet. Rights are recorded against the
// account, so the form can only name people who have logged in once.
func adminCandidates(users []database.User) []database.User {
	out := make([]database.User, 0, len(users))
	for _, u := range users {
		if !u.IsAdmin {
			out = append(out, u)
		}
	}
	return out
}
