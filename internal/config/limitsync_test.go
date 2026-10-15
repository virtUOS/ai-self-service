package config

import (
	"testing"
	"time"
)

// One worker and a five-minute retry are the conservative defaults: a large
// change takes a while to reach every key but never floods the gateway.
func TestLimitSyncDefaults(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LimitSyncWorkers != 1 {
		t.Errorf("LimitSyncWorkers = %d, want 1", cfg.LimitSyncWorkers)
	}
	if cfg.LimitSyncInterval != 5*time.Minute {
		t.Errorf("LimitSyncInterval = %v, want 5m", cfg.LimitSyncInterval)
	}
}

func TestLimitSyncSettingsAreRead(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("LIMIT_SYNC_WORKERS", "4")
	t.Setenv("LIMIT_SYNC_INTERVAL", "90s")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LimitSyncWorkers != 4 || cfg.LimitSyncInterval != 90*time.Second {
		t.Errorf("got %d workers every %v, want 4 every 90s", cfg.LimitSyncWorkers, cfg.LimitSyncInterval)
	}
}

func TestLimitSyncRejectsNonsense(t *testing.T) {
	for _, tc := range []struct{ env, value string }{
		{"LIMIT_SYNC_WORKERS", "0"},
		{"LIMIT_SYNC_WORKERS", "many"},
		{"LIMIT_SYNC_INTERVAL", "0s"},
		{"LIMIT_SYNC_INTERVAL", "-1m"},
		{"LIMIT_SYNC_INTERVAL", "often"},
	} {
		t.Run(tc.env+"="+tc.value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tc.env, tc.value)
			if _, err := Load(); err == nil {
				t.Errorf("Load accepted %s=%s", tc.env, tc.value)
			}
		})
	}
}

// A key the gateway lacks is kept for a day by default: long enough to fix a
// wrong or restored gateway, short enough not to leave dead keys around.
func TestKeyMissingGracePeriodDefault(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeyMissingGracePeriod != 24*time.Hour {
		t.Errorf("KeyMissingGracePeriod = %v, want 24h", cfg.KeyMissingGracePeriod)
	}
}

func TestKeyMissingGracePeriodIsRead(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("KEY_MISSING_GRACE_PERIOD", "72h")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeyMissingGracePeriod != 72*time.Hour {
		t.Errorf("KeyMissingGracePeriod = %v, want 72h", cfg.KeyMissingGracePeriod)
	}
}

func TestKeyMissingGracePeriodRejectsNonsense(t *testing.T) {
	for _, value := range []string{"0s", "-1h", "a day", "24"} {
		t.Run(value, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("KEY_MISSING_GRACE_PERIOD", value)
			if _, err := Load(); err == nil {
				t.Errorf("Load accepted KEY_MISSING_GRACE_PERIOD=%s", value)
			}
		})
	}
}
