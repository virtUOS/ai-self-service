package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/virtuos/ai-self-service/internal/i18n"
)

// limitSyncView is the admin panel's card on the limit sync: whether every key
// carries its profile's limits yet, and if not, how far the sync has got. It
// is shown to every admin, so one admin can see that another's change is still
// being applied.
type limitSyncView struct {
	// OK means nothing is out of date.
	OK bool
	// Unavailable means the status could not be read.
	Unavailable bool
	// Summary is the headline sentence; Details are the sentences under it.
	Summary string
	Details []string

	Failures     []limitSyncFailureRow
	MoreFailures string

	// PendingByProfile feeds the badge in the profile table.
	PendingByProfile map[int64]int
}

type limitSyncFailureRow struct {
	Email     string
	KeyPrefix string
	Error     string
	FailedAt  string
}

// limitSyncStatus builds the card from the database and the running sync.
func (a *Admin) limitSyncStatus(ctx context.Context, lang i18n.Lang) limitSyncView {
	st, err := a.store.GetLimitSyncStatus(ctx)
	if err != nil {
		slog.Error("limit sync status", "err", err)
		return limitSyncView{Unavailable: true, Summary: i18n.T(lang, "admin.sync.unavailable")}
	}

	var running bool
	var finished time.Time
	if a.sync != nil {
		running, finished = a.sync.Status()
	}

	v := limitSyncView{PendingByProfile: st.PendingByProfile}
	if st.Pending == 0 {
		v.OK = true
		v.Summary = i18n.T(lang, "admin.sync.ok")
	} else {
		v.Summary = fmt.Sprintf(i18n.T(lang, "admin.sync.pending"), st.Pending)
		if running {
			v.Details = append(v.Details, i18n.T(lang, "admin.sync.running"))
		} else {
			v.Details = append(v.Details,
				fmt.Sprintf(i18n.T(lang, "admin.sync.next"), formatInterval(a.cfg.LimitSyncInterval)))
		}
		if st.Failed > 0 {
			v.Details = append(v.Details, fmt.Sprintf(i18n.T(lang, "admin.sync.failed"), st.Failed))
		}
	}
	if !finished.IsZero() {
		v.Details = append(v.Details,
			fmt.Sprintf(i18n.T(lang, "admin.sync.last"), finished.Format("2006-01-02 15:04")))
	}

	for _, f := range st.RecentFailures {
		v.Failures = append(v.Failures, limitSyncFailureRow{
			Email:     f.Email,
			KeyPrefix: f.KeyPrefix,
			Error:     f.Error,
			FailedAt:  f.FailedAt.Local().Format("2006-01-02 15:04"),
		})
	}
	if more := st.Failed - len(st.RecentFailures); more > 0 {
		v.MoreFailures = fmt.Sprintf(i18n.T(lang, "admin.sync.more"), more)
	}
	return v
}

// formatInterval renders the retry interval for people: "5 min", not "5m0s".
func formatInterval(d time.Duration) string {
	switch {
	case d <= 0:
		return "?"
	case d%time.Hour == 0:
		return fmt.Sprintf("%d h", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%d min", d/time.Minute)
	case d%time.Second == 0:
		return fmt.Sprintf("%d s", d/time.Second)
	default:
		return d.String()
	}
}
