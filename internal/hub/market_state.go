package hub

import "time"

type MarketStatus int

const (
	MarketTrading  MarketStatus = iota
	MarketResolved
)

func (s MarketStatus) String() string {
	switch s {
	case MarketTrading:
		return "trading"
	case MarketResolved:
		return "resolved"
	default:
		return "unknown"
	}
}

// MarketState tracks the lifecycle of a single BTC 5-min market.
type MarketState struct {
	ID             string
	Slug           string
	Question       string
	UpTokenID      string
	DownTokenID    string
	StartTime      time.Time
	EndTime        time.Time
	Status         MarketStatus
	WinningOutcome string
	ResolvedAt     int64
}

func (m *MarketState) TimeToExpiry() time.Duration {
	d := time.Until(m.EndTime)
	if d < 0 {
		return 0
	}
	return d
}

func (m *MarketState) IsActive() bool {
	now := time.Now()
	return m.Status == MarketTrading && !now.Before(m.StartTime) && now.Before(m.EndTime)
}

func (m *MarketState) ContainsAsset(assetID string) bool {
	return m.UpTokenID == assetID || m.DownTokenID == assetID
}
