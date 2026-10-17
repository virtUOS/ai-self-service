package devseed

// The profiles and personas the seed creates. Between them they reach every
// state the dashboard and the admin panel can show, so a browser test can
// pick the persona that is in the state it checks.
//
// The browser tests rely on them: a test that changes data uses only the
// personas and profiles set aside for it (keyflow, logout, Scratch, Unused),
// and the others stay as seeded. A change here is a change to the tests'
// fixtures.

// QuotaWindow is one allowance window of a profile.
type QuotaWindow struct {
	Budget float64
	Period string // "1h", "24h", "7d" or "30d"
}

// ProfileSpec is a profile as the admin form creates it.
type ProfileSpec struct {
	Name        string
	Description string
	Models      []string
	TPM, RPM    int64
	KeyDays     int
	Quotas      []QuotaWindow
	// Default marks the profile the migration creates, which the seed updates
	// rather than creates.
	Default bool
}

// DefaultProfile is the name the migration gives the default profile.
const DefaultProfile = "default"

// Profiles in the order they are created. Budgets are in the mock models'
// cost units: a short request costs about 0.05 on mock-small and 0.5 on
// mock-large (dev/litellm-config.yaml).
var Profiles = []ProfileSpec{
	{
		Name:        DefaultProfile,
		Description: "Everyone without a profile of their own.",
		Quotas:      []QuotaWindow{{5, "24h"}},
		Default:     true,
	},
	{
		// 24h rather than 1h: a 1h window resets at the top of every hour,
		// and the bar would empty in the middle of a test run.
		Name:        "Tight",
		Description: "A small daily budget, nearly used up by heavy.",
		Quotas:      []QuotaWindow{{1, "24h"}},
	},
	{
		Name:        "Stacked",
		Description: "Three windows at once.",
		Quotas:      []QuotaWindow{{2, "1h"}, {5, "24h"}, {10, "7d"}},
	},
	{
		Name:        "Small only",
		Description: "mock-small only, with rate limits.",
		Models:      []string{"mock-small"},
		TPM:         1000,
		RPM:         10,
		Quotas:      []QuotaWindow{{3, "24h"}},
	},
	{
		Name:        "Short keys",
		Description: "Keys valid for three days.",
		KeyDays:     3,
	},
	{
		Name:        "Scratch",
		Description: "Edited by the browser tests.",
	},
	{
		Name:        "Unused",
		Description: "No users; deleted by the browser tests.",
	},
}

// Persona is a user of the seeded instance. Their name, email and groups come
// from dev/mock-users.json, which the OIDC mock serves; the persona says what
// the seed does with them.
type Persona struct {
	Sub string
	// Profile is assigned explicitly; empty follows the default.
	Profile string
	// UntilDays sets a deadline on the assignment, in days from today. A
	// negative value is a deadline that has already passed, which the server
	// applies when it restarts.
	UntilDays int
	// After is the profile at the deadline; empty is the default.
	After          string
	RevokeAtExpiry bool

	// Key generates a key, and Requests are the models it sends one request
	// each to.
	Key      bool
	Requests []string
	// ExpiredKey backdates the key, and MissingKey deletes it in the gateway.
	ExpiredKey bool
	MissingKey bool

	// Shows is what the persona is for, as the seed prints it.
	Shows string
}

// Personas in the order they are seeded.
var Personas = []Persona{
	{Sub: "admin", Shows: "admin (in the keyosk-admin group)"},
	{Sub: "admin2", Shows: "a second admin"},
	{Sub: "student", Key: true,
		Requests: []string{"mock-small", "mock-small", "mock-small", "mock-large"},
		Shows:    "a key with usage on two models"},
	{Sub: "nokey", Shows: "no key yet"},
	{Sub: "keyflow", Shows: "no key; for the browser tests' key flow"},
	{Sub: "logout", Shows: "no key; for the browser tests' logout"},
	{Sub: "heavy", Profile: "Tight", Key: true,
		Requests: []string{"mock-large", "mock-large"},
		Shows:    "a nearly full quota bar"},
	{Sub: "stacked", Profile: "Stacked", Key: true,
		Requests: []string{"mock-small", "mock-small", "mock-large"},
		Shows:    "three quota bars"},
	{Sub: "restricted", Profile: "Small only", Key: true,
		Requests: []string{"mock-small", "mock-small", "mock-small"},
		Shows:    "one model, with rate limits"},
	{Sub: "expiring", Profile: "Short keys", Key: true,
		Requests: []string{"mock-small"},
		Shows:    "a key that expires in three days"},
	{Sub: "expired", Key: true, ExpiredKey: true,
		Requests: []string{"mock-small"},
		Shows:    "an expired key"},
	{Sub: "missing", Key: true, MissingKey: true,
		Shows: "a key the gateway no longer has"},
	{Sub: "guest", Profile: "Stacked", UntilDays: 7, RevokeAtExpiry: true, Key: true,
		Requests: []string{"mock-small"},
		Shows:    "Stacked for a week, then the key is revoked"},
	{Sub: "intern", Profile: "Small only", UntilDays: 30, After: "Tight", Key: true,
		Shows: "Small only for a month, then Tight"},
	{Sub: "lapsed", Profile: "Stacked", UntilDays: -1, Key: true,
		Shows: "an assignment that ran out, back on the default"},
	{Sub: "oezlem", Key: true, Requests: []string{"mock-small"},
		Shows: "a name with umlauts and a long address"},
}
