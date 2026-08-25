package litellm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"

	"github.com/virtuos/ai-self-service/internal/keyprovider"
)

// spendRow is one request in LiteLLM's spend log.
//
// Only the fields the portal aggregates are declared; the row carries plenty
// more (model, latency, prompt/completion split) that nothing here needs.
type spendRow struct {
	APIKey      string `json:"api_key"`
	TotalTokens int64  `json:"total_tokens"`
	StartTime   string `json:"startTime"`
}

// keyHash is how LiteLLM identifies a key in its spend log: the SHA-256 of the
// key itself. The portal stores the key, so it derives this rather than
// keeping a second copy of the same fact.
func keyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// Usage returns per-day token totals for a key, oldest first.
//
// It reads the raw per-request log and aggregates here. LiteLLM will aggregate
// by day itself when given start_date/end_date, but that response reports
// spend only and drops token counts entirely — and local models are priced so
// that spend is always zero, so it carries no usable signal.
func (c *Client) Usage(ctx context.Context, key string, days int) ([]keyprovider.DailyUsage, error) {
	q := url.Values{}
	q.Set("api_key", keyHash(key))

	resp, err := c.do(ctx, http.MethodGet, "/spend/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("LiteLLM /spend/logs returned %d: %s", resp.StatusCode, b)
	}

	var rows []spendRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("decode spend log: %w", err)
	}

	// Bound the window here rather than in the query: start_date/end_date
	// switch the endpoint to its aggregated shape, which has no token counts.
	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	totals := make(map[string]int64)
	for _, r := range rows {
		if r.TotalTokens <= 0 || len(r.StartTime) < 10 {
			continue
		}
		day := r.StartTime[:10]
		if day < cutoff {
			continue
		}
		totals[day] += r.TotalTokens
	}

	out := make([]keyprovider.DailyUsage, 0, len(totals))
	for day, tokens := range totals {
		out = append(out, keyprovider.DailyUsage{Day: day, Tokens: tokens})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out, nil
}
