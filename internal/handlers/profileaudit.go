package handlers

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/virtuos/keyosk/internal/database"
)

// describeProfile summarises a profile for the audit log when it is created
// or deleted, e.g. "models a, b; tpm 1000; quotas 24h 5, 7d 20; valid 90d;
// default". Limits that are not set are left out; models and quotas are
// always named, since "all" and "unlimited" are what matter about them.
func describeProfile(p *database.Profile, quotas []database.ProfileQuota) string {
	parts := []string{"models " + fmtModels(p.Models)}
	if p.TPMLimit != nil {
		parts = append(parts, "tpm "+fmtLimit(p.TPMLimit))
	}
	if p.RPMLimit != nil {
		parts = append(parts, "rpm "+fmtLimit(p.RPMLimit))
	}
	parts = append(parts, "quotas "+fmtQuotas(quotas))
	if p.KeyDurationDays > 0 {
		parts = append(parts, "valid "+fmtKeyDays(p.KeyDurationDays))
	}
	if p.IsDefault {
		parts = append(parts, "default")
	}
	return strings.Join(parts, "; ")
}

// diffProfile lists what an update changed, e.g. "name: old → new; models:
// a → a, b; quota 24h: 5 → 10". It is empty when the save changed nothing.
// prev carries the windows before the save, quotas those after it.
func diffProfile(prev, next *database.Profile, quotas []database.ProfileQuota) string {
	var parts []string
	change := func(field, from, to string) {
		if from != to {
			parts = append(parts, field+": "+from+" → "+to)
		}
	}

	change("name", prev.Name, next.Name)
	if prev.Description != next.Description {
		// The text can be long, and the table has no room for it twice.
		parts = append(parts, "description changed")
	}
	change("models", fmtModels(prev.Models), fmtModels(next.Models))
	change("tpm", fmtLimit(prev.TPMLimit), fmtLimit(next.TPMLimit))
	change("rpm", fmtLimit(prev.RPMLimit), fmtLimit(next.RPMLimit))
	change("valid", fmtKeyDays(prev.KeyDurationDays), fmtKeyDays(next.KeyDurationDays))

	// Windows are matched by period, so their order does not count as a change.
	before, after := quotasByPeriod(prev.Quotas), quotasByPeriod(quotas)
	for _, period := range quotaPeriods(before, after) {
		change("quota "+period, fmtBudget(before, period), fmtBudget(after, period))
	}

	// The default cannot be un-defaulted, so only the other way round happens.
	if !prev.IsDefault && next.IsDefault {
		parts = append(parts, "now default")
	}
	return strings.Join(parts, "; ")
}

func fmtModels(models []string) string {
	if len(models) == 0 {
		return "all"
	}
	return strings.Join(models, ", ")
}

func fmtLimit(v *int64) string {
	if v == nil {
		return "unlimited"
	}
	return strconv.FormatInt(*v, 10)
}

func fmtKeyDays(days int) string {
	if days == 0 {
		return "server default"
	}
	return fmt.Sprintf("%dd", days)
}

func fmtQuotas(quotas []database.ProfileQuota) string {
	if len(quotas) == 0 {
		return "unlimited"
	}
	byPeriod := quotasByPeriod(quotas)
	parts := make([]string, 0, len(quotas))
	for _, period := range quotaPeriods(byPeriod) {
		parts = append(parts, period+" "+fmtBudget(byPeriod, period))
	}
	return strings.Join(parts, ", ")
}

func fmtBudget(byPeriod map[string]float64, period string) string {
	budget, ok := byPeriod[period]
	if !ok {
		return "none"
	}
	return fmt.Sprintf("%g", budget)
}

func quotasByPeriod(quotas []database.ProfileQuota) map[string]float64 {
	m := make(map[string]float64, len(quotas))
	for _, q := range quotas {
		m[q.Period] = q.Budget
	}
	return m
}

// quotaPeriods returns the periods present in any of the maps, shortest first.
func quotaPeriods(maps ...map[string]float64) []string {
	var periods []string
	for _, m := range maps {
		for period := range m {
			if !slices.Contains(periods, period) {
				periods = append(periods, period)
			}
		}
	}
	slices.SortFunc(periods, func(a, b string) int {
		return periodHours[a] - periodHours[b]
	})
	return periods
}
