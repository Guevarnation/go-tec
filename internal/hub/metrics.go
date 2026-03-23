package hub

import (
	"strings"
	"time"
)

// TradeEntry records a single trade execution for VWAP and velocity calculations.
type TradeEntry struct {
	Price     float64
	Size      float64
	Side      string
	Timestamp int64
}

// TradeBuffer is a fixed-capacity ring buffer for recent trades on a single asset.
type TradeBuffer struct {
	data []TradeEntry
	head int
	len  int
	cap  int
}

func NewTradeBuffer(capacity int) *TradeBuffer {
	return &TradeBuffer{
		data: make([]TradeEntry, capacity),
		cap:  capacity,
	}
}

func (b *TradeBuffer) Add(t TradeEntry) {
	b.data[b.head] = t
	b.head = (b.head + 1) % b.cap
	if b.len < b.cap {
		b.len++
	}
}

func (b *TradeBuffer) Len() int { return b.len }

// RecentSince returns trades with timestamp >= sinceUnix, from newest to oldest.
func (b *TradeBuffer) RecentSince(sinceUnix int64) []TradeEntry {
	result := make([]TradeEntry, 0, b.len)
	for i := 0; i < b.len; i++ {
		idx := (b.head - 1 - i + b.cap*2) % b.cap
		if b.data[idx].Timestamp < sinceUnix {
			break
		}
		result = append(result, b.data[idx])
	}
	return result
}

// VWAP computes volume-weighted average price over the given window.
func (b *TradeBuffer) VWAP(window time.Duration) (float64, bool) {
	since := time.Now().Unix() - int64(window.Seconds())
	trades := b.RecentSince(since)
	if len(trades) == 0 {
		return 0, false
	}
	var sumPV, sumV float64
	for _, t := range trades {
		sumPV += t.Price * t.Size
		sumV += t.Size
	}
	if sumV == 0 {
		return 0, false
	}
	return sumPV / sumV, true
}

// Velocity returns trades per second over the given window.
func (b *TradeBuffer) Velocity(window time.Duration) float64 {
	since := time.Now().Unix() - int64(window.Seconds())
	trades := b.RecentSince(since)
	secs := window.Seconds()
	if secs == 0 {
		return 0
	}
	return float64(len(trades)) / secs
}

// BuySellVolume returns total buy and sell volume over the given window.
func (b *TradeBuffer) BuySellVolume(window time.Duration) (buyVol, sellVol float64) {
	since := time.Now().Unix() - int64(window.Seconds())
	trades := b.RecentSince(since)
	for _, t := range trades {
		switch strings.ToLower(t.Side) {
		case "buy":
			buyVol += t.Size
		case "sell":
			sellVol += t.Size
		}
	}
	return
}
