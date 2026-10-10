package config

import (
	"net/netip"
	"slices"
	"testing"
)

func prefixStrings(ps []netip.Prefix) []string {
	s := make([]string, len(ps))
	for i, p := range ps {
		s[i] = p.String()
	}
	return s
}

// Unset trusts the loopback and private ranges, where a proxy in front of
// the portal usually sits.
func TestTrustedProxiesDefault(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := prefixStrings(cfg.TrustedProxies); !slices.Equal(got, defaultTrustedProxies) {
		t.Errorf("TrustedProxies = %v, want %v", got, defaultTrustedProxies)
	}
}

func TestTrustedProxiesAreRead(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TRUSTED_PROXIES", " 127.0.0.1, 192.0.2.17/24,,2001:db8::/32 , ::ffff:198.51.100.1")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"127.0.0.1/32", "192.0.2.0/24", "2001:db8::/32", "198.51.100.1/32"}
	if got := prefixStrings(cfg.TrustedProxies); !slices.Equal(got, want) {
		t.Errorf("TrustedProxies = %v, want %v", got, want)
	}
}

func TestTrustedProxiesNone(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("TRUSTED_PROXIES", "none")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want none", cfg.TrustedProxies)
	}
}

func TestTrustedProxiesRejectNonsense(t *testing.T) {
	for _, v := range []string{"10.0.0.0/33", "banana", "10.0.0.1/8x", "fe80::1%eth0", "10.0.0.0/8, nope"} {
		t.Run(v, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("TRUSTED_PROXIES", v)
			if _, err := Load(); err == nil {
				t.Errorf("Load accepted TRUSTED_PROXIES=%s", v)
			}
		})
	}
}
