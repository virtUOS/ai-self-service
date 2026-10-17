package devseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/litellm"
)

// Manifest describes a seeded instance: where it runs, how its users are
// named in LiteLLM, and who is in it.
type Manifest struct {
	URL    string         `json:"url"`
	Prefix string         `json:"prefix"`
	Users  []ManifestUser `json:"users"`
}

// ManifestUser is a persona as seeded.
type ManifestUser struct {
	Sub     string `json:"sub"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Profile string `json:"profile"`
	Shows   string `json:"shows"`
}

// mockUser is an entry of dev/mock-users.json.
type mockUser struct {
	Sub    string   `json:"sub"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Groups []string `json:"groups"`
}

// GatewayURL and GatewayKey are the LiteLLM gateway the instance uses, as
// .env.example sets it, unless LITELLM_BASE_URL and LITELLM_MASTER_KEY
// override it for both.
func GatewayURL() string { return envOr("LITELLM_BASE_URL", "http://localhost:4000") }

// GatewayKey is the gateway's master key; see GatewayURL.
func GatewayKey() string { return envOr("LITELLM_MASTER_KEY", "sk-local-dev") }

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

// Seed starts the instance and fills it with Profiles and Personas. It leaves
// the instance running.
//
// Everything a user or an admin could do goes through the app's own pages, so
// keys, limits and the audit log come out as they would in production. Only
// what nobody can do there is written to the database, with the server
// stopped: a key that has expired and a deadline that has passed. A restart
// then empties the usage cache, which would otherwise hold the "no usage" the
// key's first page load saw, and lets the expiry job apply the deadline.
func Seed(ctx context.Context, inst *Instance, out io.Writer) (*Manifest, error) {
	mock, err := readMockUsers(inst.MockUsersPath())
	if err != nil {
		return nil, err
	}
	waitOutTheHour(ctx, out)

	if err := inst.Start(ctx); err != nil {
		return nil, err
	}
	db, err := database.Open(inst.DBPath())
	if err != nil {
		return nil, err
	}
	store := database.NewStore(db)
	defer store.Close()
	gw := litellm.NewClient(GatewayURL(), GatewayKey())
	s := &seeder{inst: inst, store: store, gw: gw, out: out, clients: map[string]*Client{}}

	fmt.Fprintf(out, "Seeding %s (server log: %s)\n", inst.URL(), inst.LogPath())
	steps := []struct {
		what string
		run  func(context.Context) error
	}{
		{"signing everyone in", func(ctx context.Context) error { return s.login(ctx, mock) }},
		{"creating the profiles", s.profiles},
		{"assigning them", s.assign},
		{"generating keys and sending requests", s.keys},
		{"waiting for LiteLLM to record the spend", s.waitForSpend},
		{"expiring a key and an assignment", s.backdate},
		{"marking the missing key", s.markMissing},
	}
	for _, step := range steps {
		fmt.Fprintf(out, "  %s\n", step.what)
		if err := step.run(ctx); err != nil {
			return nil, fmt.Errorf("%s: %w", step.what, err)
		}
	}

	m := &Manifest{URL: inst.URL(), Prefix: inst.Prefix}
	for _, p := range Personas {
		profile := p.Profile
		if p.UntilDays < 0 || profile == "" {
			profile = DefaultProfile
		}
		u := mock[p.Sub]
		m.Users = append(m.Users, ManifestUser{
			Sub: p.Sub, Name: u.Name, Email: u.Email, Profile: profile, Shows: p.Shows,
		})
	}
	return m, nil
}

type seeder struct {
	inst    *Instance
	store   *database.Store
	gw      *litellm.Client
	out     io.Writer
	clients map[string]*Client
}

// login signs every persona in once: an account exists only from its first
// login, and the admin can assign a profile only to an account that exists.
func (s *seeder) login(ctx context.Context, mock map[string]mockUser) error {
	for _, p := range Personas {
		want, ok := mock[p.Sub]
		if !ok {
			return fmt.Errorf("%s is not in %s", p.Sub, s.inst.MockUsersPath())
		}
		c := NewClient(s.inst.URL())
		if err := c.Login(ctx, p.Sub); err != nil {
			return err
		}
		// The mock reads its users file when it starts, and accepts any
		// subject; one it does not know signs in without an address.
		u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
		if err != nil {
			return err
		}
		if u.Email != want.Email {
			return fmt.Errorf("%s signed in as %q, not %q: the OIDC mock has not read the current %s; "+
				"restart it with `docker compose -f dev/docker-compose.yml restart oidc-mock` (or podman compose)",
				p.Sub, u.Email, want.Email, s.inst.MockUsersPath())
		}
		s.clients[p.Sub] = c
	}
	return nil
}

func (s *seeder) admin(ctx context.Context) (*Client, error) {
	c := s.clients["admin"]
	_, err := c.Page(ctx, "/admin")
	return c, err
}

func (s *seeder) profiles(ctx context.Context) error {
	admin, err := s.admin(ctx)
	if err != nil {
		return err
	}
	for _, p := range Profiles {
		form := url.Values{
			"name":        {p.Name},
			"description": {p.Description},
			"model":       p.Models,
		}
		if p.TPM > 0 {
			form.Set("tpm_limit", strconv.FormatInt(p.TPM, 10))
		}
		if p.RPM > 0 {
			form.Set("rpm_limit", strconv.FormatInt(p.RPM, 10))
		}
		if p.KeyDays > 0 {
			form.Set("key_duration_days", strconv.Itoa(p.KeyDays))
		}
		for _, q := range p.Quotas {
			form.Add("quota_budget", strconv.FormatFloat(q.Budget, 'f', -1, 64))
			form.Add("quota_period", q.Period)
		}
		if !p.Default {
			if err := admin.Admin(ctx, "/admin/profiles", form, "Profile created"); err != nil {
				return err
			}
			continue
		}
		def, err := s.store.GetDefaultProfile(ctx)
		if err != nil {
			return err
		}
		// The default profile must stay the default, and the form says so.
		form.Set("is_default", "on")
		if err := admin.Admin(ctx, fmt.Sprintf("/admin/profiles/%d", def.ID), form, "Profile updated"); err != nil {
			return err
		}
	}
	return nil
}

func (s *seeder) profileIDs(ctx context.Context) (map[string]int64, error) {
	profiles, err := s.store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, p := range profiles {
		ids[p.Name] = p.ID
	}
	return ids, nil
}

// assign gives personas their profiles. It comes before the keys, which are
// created with their owner's limits: a key generated first would carry the
// default profile's until the next limit sync, and the requests sent with it
// would count against the wrong budget.
func (s *seeder) assign(ctx context.Context) error {
	admin, err := s.admin(ctx)
	if err != nil {
		return err
	}
	ids, err := s.profileIDs(ctx)
	if err != nil {
		return err
	}
	today := time.Now()
	for _, p := range Personas {
		if p.Profile == "" {
			continue
		}
		u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
		if err != nil {
			return err
		}
		form := url.Values{"profile_id": {strconv.FormatInt(ids[p.Profile], 10)}}
		if p.UntilDays != 0 {
			// A deadline that has passed is set for tomorrow here and moved
			// back with the server stopped: set from the panel, the running
			// server could apply it before the key is generated.
			days := max(p.UntilDays, 1)
			form.Set("expires_at", today.AddDate(0, 0, days).Format("2006-01-02"))
			if p.After != "" {
				form.Set("after_expiry", strconv.FormatInt(ids[p.After], 10))
			}
			if p.RevokeAtExpiry {
				form.Set("revoke_at_expiry", "on")
			}
		}
		if err := admin.Admin(ctx, fmt.Sprintf("/admin/users/%d/profile", u.ID), form, "User profile updated"); err != nil {
			return fmt.Errorf("%s: %w", p.Sub, err)
		}
	}
	return nil
}

func (s *seeder) keys(ctx context.Context) error {
	for _, p := range Personas {
		if !p.Key {
			continue
		}
		c := s.clients[p.Sub]
		secret, err := c.GenerateKey(ctx)
		if err != nil {
			return fmt.Errorf("%s: %w", p.Sub, err)
		}
		for _, model := range p.Requests {
			if err := Complete(ctx, GatewayURL(), secret, model); err != nil {
				return fmt.Errorf("%s: %w", p.Sub, err)
			}
		}
		if p.MissingKey {
			k, err := s.key(ctx, p.Sub)
			if err != nil {
				return err
			}
			// What an admin deleting it in LiteLLM's own UI would do.
			if err := s.gw.DeleteKey(ctx, k.KeyRef); err != nil {
				return fmt.Errorf("%s: delete the key in LiteLLM: %w", p.Sub, err)
			}
		}
	}
	return nil
}

func (s *seeder) key(ctx context.Context, sub string) (*database.APIKey, error) {
	u, err := s.store.GetUserByOIDCSub(ctx, sub)
	if err != nil {
		return nil, err
	}
	k, err := s.store.GetAPIKeyByUser(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sub, err)
	}
	return k, nil
}

// waitForSpend waits until LiteLLM reports each key's spend and it has stopped
// growing. LiteLLM writes spend in batches, a few seconds after the requests.
func (s *seeder) waitForSpend(ctx context.Context) error {
	deadline := time.Now().Add(90 * time.Second)
	for _, p := range Personas {
		if len(p.Requests) == 0 {
			continue
		}
		k, err := s.key(ctx, p.Sub)
		if err != nil {
			return err
		}
		last, steady := -1.0, 0
		for steady < 3 {
			if time.Now().After(deadline) {
				return fmt.Errorf("%s: LiteLLM recorded no spend within 90s", p.Sub)
			}
			q, err := s.gw.KeyQuota(ctx, k.KeyRef)
			if err != nil {
				return fmt.Errorf("%s: %w", p.Sub, err)
			}
			if q.Used > 0 && q.Used == last {
				steady++
			} else {
				steady = 0
			}
			last = q.Used
			if err := sleep(ctx, time.Second); err != nil {
				return err
			}
		}
	}
	return nil
}

// backdate writes, with the server stopped, the two states nobody can reach
// from the app's pages, and starts the server again.
func (s *seeder) backdate(ctx context.Context) error {
	if err := s.inst.Stop(); err != nil {
		return err
	}
	now := time.Now()
	for _, p := range Personas {
		if p.ExpiredKey {
			k, err := s.key(ctx, p.Sub)
			if err != nil {
				return err
			}
			if err := s.store.UpdateAPIKeyExpiry(ctx, k.ID, now.AddDate(0, 0, -3)); err != nil {
				return err
			}
		}
		if p.UntilDays < 0 {
			u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
			if err != nil {
				return err
			}
			until := now.AddDate(0, 0, p.UntilDays)
			if err := s.store.SetUserProfileUntil(ctx, u.ID, u.ProfileID, &until,
				u.ProfileAfterExpiry, u.RevokeKeyAtExpiry); err != nil {
				return err
			}
		}
	}
	if err := s.inst.Start(ctx); err != nil {
		return err
	}
	// The expiry job runs right after startup, alongside the server.
	for _, p := range Personas {
		if p.UntilDays >= 0 {
			continue
		}
		if err := poll(ctx, 15*time.Second, func() (bool, error) {
			u, err := s.store.GetUserByOIDCSub(ctx, p.Sub)
			return err == nil && u.ProfileExpiresAt == nil, err
		}); err != nil {
			return fmt.Errorf("%s: the expiry job did not apply the deadline: %w", p.Sub, err)
		}
	}
	return nil
}

// markMissing lets the app find out that the gateway lost a key, which it does
// when the owner's dashboard asks the gateway for its usage.
func (s *seeder) markMissing(ctx context.Context) error {
	for _, p := range Personas {
		if !p.MissingKey {
			continue
		}
		if _, err := s.clients[p.Sub].Page(ctx, "/"); err != nil {
			return fmt.Errorf("%s: %w", p.Sub, err)
		}
		k, err := s.key(ctx, p.Sub)
		if err != nil {
			return err
		}
		if k.MissingSince == nil {
			return fmt.Errorf("%s: the dashboard did not mark the key missing", p.Sub)
		}
	}
	return nil
}

// waitOutTheHour waits for the next hour when it is only minutes away. Quota
// windows reset on the clock, the hourly ones on the hour and the daily ones
// at midnight UTC, and one that reset during the seed would show none of the
// usage it just made.
func waitOutTheHour(ctx context.Context, out io.Writer) {
	now := time.Now()
	next := now.Truncate(time.Hour).Add(time.Hour)
	if wait := next.Sub(now); wait < 3*time.Minute {
		wait += 15 * time.Second
		fmt.Fprintf(out, "Waiting %v for the quota windows to reset on the hour first\n", wait.Round(time.Second))
		_ = sleep(ctx, wait)
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func poll(ctx context.Context, timeout time.Duration, done func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		ok, err := done()
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			if err == nil {
				err = errors.New("timed out")
			}
			return err
		}
		if err := sleep(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
}

func readMockUsers(path string) (map[string]mockUser, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var list []mockUser
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	users := map[string]mockUser{}
	for _, u := range list {
		users[u.Sub] = u
	}
	return users, nil
}

// Write saves the manifest as JSON.
func (m *Manifest) Write(path string) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// ReadManifest loads a manifest that Write saved.
func ReadManifest(path string) (*Manifest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

// Cleanup deletes what a seeded instance created in LiteLLM: the keys its
// database still knows, and its users, which hold the spend and any key the
// database no longer knows. The instance's own files stay.
func Cleanup(ctx context.Context, dbPath, prefix string) error {
	if prefix == "" {
		return errors.New("cleanup needs the run's prefix, or it would name users of no run")
	}
	gw := litellm.NewClient(GatewayURL(), GatewayKey())
	var errs []error

	db, err := database.Open(dbPath)
	if err == nil {
		store := database.NewStore(db)
		keys, err := store.ListAPIKeys(ctx)
		if err != nil {
			errs = append(errs, err)
		}
		for _, k := range keys {
			if err := gw.DeleteKey(ctx, k.KeyRef); err != nil {
				errs = append(errs, err)
			}
		}
		store.Close()
	} else {
		errs = append(errs, err)
	}

	ids := make([]string, 0, len(Personas))
	for _, p := range Personas {
		ids = append(ids, prefix+":"+p.Sub)
	}
	if err := deleteUsers(ctx, ids); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// deleteUsers deletes gateway users; the portal's client has no call for it,
// since the portal never deletes one. One at a time, because LiteLLM refuses
// the whole list when one of them does not exist, and only personas who
// generated a key have a user there.
func deleteUsers(ctx context.Context, ids []string) error {
	var errs []error
	for _, id := range ids {
		body, _ := json.Marshal(map[string]any{"user_ids": []string{id}})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			strings.TrimRight(GatewayURL(), "/")+"/user/delete", bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+GatewayKey())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("delete gateway user %s: %w", id, err)
		}
		b, _ := readBody(resp)
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
			errs = append(errs, fmt.Errorf("delete gateway user %s: %d: %.200s", id, resp.StatusCode, b))
		}
	}
	return errors.Join(errs...)
}
