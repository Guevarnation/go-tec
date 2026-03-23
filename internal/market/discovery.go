package market

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const windowDuration = 5 * time.Minute

type ActiveMarket struct {
	ID          string
	Question    string
	Slug        string
	ConditionID string
	StartTime   time.Time
	EndTime     time.Time
	UpTokenID   string
	DownTokenID string
	Closed      bool
}

type Discovery struct {
	baseURL string
	logger  *slog.Logger
	prefix  string
	client  *http.Client
}

func NewDiscovery(gammaURL, slugPrefix string, logger *slog.Logger) *Discovery {
	return &Discovery{
		baseURL: gammaURL,
		logger:  logger,
		prefix:  slugPrefix,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

type gammaEvent struct {
	ID      string        `json:"id"`
	Title   string        `json:"title"`
	Slug    string        `json:"slug"`
	Active  bool          `json:"active"`
	Closed  bool          `json:"closed"`
	Markets []gammaMarket `json:"markets"`
}

type gammaMarket struct {
	ID            string          `json:"id"`
	Question      string          `json:"question"`
	ConditionID   string          `json:"conditionId"`
	Slug          string          `json:"slug"`
	EndDate       string          `json:"endDate"`
	Active        bool            `json:"active"`
	Closed        bool            `json:"closed"`
	ClobTokenIds  string          `json:"clobTokenIds"`
	Outcomes      string          `json:"outcomes"`
	OutcomePrices string          `json:"outcomePrices"`
	Tokens        json.RawMessage `json:"tokens"`
}

type gammaToken struct {
	TokenID string `json:"tokenId"`
	Outcome string `json:"outcome"`
}

// FindCurrentAndUpcoming returns the current and next few BTC 5-min markets
// by computing the expected event slugs from the current time.
func (d *Discovery) FindCurrentAndUpcoming(ctx context.Context, windows int) ([]ActiveMarket, error) {
	now := time.Now().UTC()
	baseTS := now.Unix() / 300 * 300 // round down to 5-min boundary

	var results []ActiveMarket
	for i := 0; i < windows; i++ {
		ts := baseTS + int64(i)*300
		slug := fmt.Sprintf("%s-%d", d.prefix, ts)

		event, err := d.fetchEvent(ctx, slug)
		if err != nil {
			d.logger.Debug("event not found", "slug", slug, "err", err)
			continue
		}

		for _, m := range event.Markets {
			am, err := toActiveMarket(m, ts)
			if err != nil {
				d.logger.Warn("skipping market", "slug", m.Slug, "err", err)
				continue
			}
			am.Closed = event.Closed || m.Closed
			results = append(results, am)
		}
	}

	d.logger.Debug("discovered BTC 5m markets", "count", len(results))
	for _, m := range results {
		d.logger.Debug("market",
			"slug", m.Slug,
			"question", m.Question,
			"start", m.StartTime.Format("15:04:05"),
			"end", m.EndTime.Format("15:04:05"),
			"closed", m.Closed,
			"up_token", truncateID(m.UpTokenID),
			"down_token", truncateID(m.DownTokenID),
		)
	}

	return results, nil
}

func (d *Discovery) fetchEvent(ctx context.Context, slug string) (*gammaEvent, error) {
	params := url.Values{"slug": {slug}}
	reqURL := d.baseURL + "/events?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gamma api %d: %s", resp.StatusCode, string(body))
	}

	var events []gammaEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no event found for slug %s", slug)
	}
	return &events[0], nil
}

// CheckResolution queries the Gamma API to determine the winning outcome
// of a resolved market. startTS is the market window's start Unix timestamp.
// Returns the winning outcome ("Up"/"Down") and whether the market is resolved.
func (d *Discovery) CheckResolution(ctx context.Context, startTS int64) (string, bool, error) {
	slug := fmt.Sprintf("%s-%d", d.prefix, startTS)
	event, err := d.fetchEvent(ctx, slug)
	if err != nil {
		return "", false, err
	}

	if !event.Closed {
		return "", false, nil
	}

	for _, m := range event.Markets {
		if !m.Closed {
			continue
		}

		var outcomes []string
		var prices []string
		_ = json.Unmarshal([]byte(m.Outcomes), &outcomes)
		_ = json.Unmarshal([]byte(m.OutcomePrices), &prices)

		if len(outcomes) != len(prices) || len(outcomes) < 2 {
			continue
		}

		bestIdx := 0
		bestPrice := 0.0
		for i, ps := range prices {
			var p float64
			fmt.Sscanf(ps, "%f", &p)
			if p > bestPrice {
				bestPrice = p
				bestIdx = i
			}
		}

		if bestPrice > 0.5 {
			return outcomes[bestIdx], true, nil
		}
	}

	return "", false, nil
}

// AllAssetIDs returns all token IDs (both Up and Down) for subscribing to WS.
func AllAssetIDs(markets []ActiveMarket) []string {
	ids := make([]string, 0, len(markets)*2)
	for _, m := range markets {
		ids = append(ids, m.UpTokenID, m.DownTokenID)
	}
	return ids
}

// OpenAssetIDs returns token IDs only for non-closed markets.
func OpenAssetIDs(markets []ActiveMarket) []string {
	ids := make([]string, 0, len(markets)*2)
	for _, m := range markets {
		if !m.Closed {
			ids = append(ids, m.UpTokenID, m.DownTokenID)
		}
	}
	return ids
}

func toActiveMarket(m gammaMarket, windowTS int64) (ActiveMarket, error) {
	upID, downID, err := extractTokenIDs(m)
	if err != nil {
		return ActiveMarket{}, err
	}

	start := time.Unix(windowTS, 0).UTC()
	end := start.Add(windowDuration)

	return ActiveMarket{
		ID:          m.ID,
		Question:    m.Question,
		Slug:        m.Slug,
		ConditionID: m.ConditionID,
		StartTime:   start,
		EndTime:     end,
		UpTokenID:   upID,
		DownTokenID: downID,
		Closed:      m.Closed,
	}, nil
}

func extractTokenIDs(m gammaMarket) (upID, downID string, err error) {
	var tokens []gammaToken
	if len(m.Tokens) > 0 {
		_ = json.Unmarshal(m.Tokens, &tokens)
	}

	if len(tokens) < 2 {
		var ids []string
		if err := json.Unmarshal([]byte(m.ClobTokenIds), &ids); err != nil || len(ids) < 2 {
			return "", "", fmt.Errorf("no token IDs found")
		}
		var outcomes []string
		_ = json.Unmarshal([]byte(m.Outcomes), &outcomes)

		tokens = make([]gammaToken, len(ids))
		for i, id := range ids {
			tokens[i].TokenID = id
			if i < len(outcomes) {
				tokens[i].Outcome = outcomes[i]
			}
		}
	}

	for _, t := range tokens {
		switch strings.ToLower(t.Outcome) {
		case "up", "yes":
			upID = t.TokenID
		case "down", "no":
			downID = t.TokenID
		}
	}
	if upID == "" || downID == "" {
		return "", "", fmt.Errorf("could not identify Up/Down tokens")
	}
	return upID, downID, nil
}

func truncateID(id string) string {
	if len(id) > 16 {
		return id[:8] + "..." + id[len(id)-8:]
	}
	return id
}
