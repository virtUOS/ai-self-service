package litellm

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// nearly compares spend figures that are summed rather than passed straight
// through, so they need not be bit-identical.
func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-12 }

// A gateway that reports no models at all has nothing unpriced to warn about.
func TestPricingEmptyGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	p, err := NewClient(srv.URL, "mk").Pricing(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unpriced) != 0 {
		t.Errorf("Unpriced = %v, want none", p.Unpriced)
	}
}
