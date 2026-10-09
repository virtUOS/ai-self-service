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
