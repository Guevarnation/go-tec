package hub

type priceEntry struct {
	Price     float64
	Timestamp int64
}

// PriceBuffer is a fixed-capacity ring buffer for BTC price time-series data.
// Not thread-safe on its own; the Hub's RWMutex protects all access.
type PriceBuffer struct {
	data []priceEntry
	head int
	len  int
	cap  int
}

func NewPriceBuffer(capacity int) *PriceBuffer {
	return &PriceBuffer{
		data: make([]priceEntry, capacity),
		cap:  capacity,
	}
}

func (b *PriceBuffer) Add(price float64, ts int64) {
	b.data[b.head] = priceEntry{Price: price, Timestamp: ts}
	b.head = (b.head + 1) % b.cap
	if b.len < b.cap {
		b.len++
	}
}

func (b *PriceBuffer) Len() int { return b.len }

func (b *PriceBuffer) Latest() (price float64, ts int64, ok bool) {
	if b.len == 0 {
		return 0, 0, false
	}
	e := b.at(0)
	return e.Price, e.Timestamp, true
}

// at returns the entry at position i from most recent (0 = latest).
func (b *PriceBuffer) at(i int) priceEntry {
	idx := (b.head - 1 - i + b.cap*2) % b.cap
	return b.data[idx]
}

// SMA returns the simple moving average over the last `window` entries.
func (b *PriceBuffer) SMA(window int) (float64, bool) {
	n := min(window, b.len)
	if n == 0 {
		return 0, false
	}
	var sum float64
	for i := 0; i < n; i++ {
		sum += b.at(i).Price
	}
	return sum / float64(n), true
}

// Slope returns the linear regression slope over the last `window` entries.
// Positive = price rising, negative = falling. Used for momentum signals.
func (b *PriceBuffer) Slope(window int) (float64, bool) {
	n := min(window, b.len)
	if n < 2 {
		return 0, false
	}
	var sumX, sumY, sumXY, sumX2 float64
	for i := 0; i < n; i++ {
		x := float64(i)
		y := b.at(n - 1 - i).Price // oldest first so x=0 is oldest
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	nf := float64(n)
	denom := nf*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, false
	}
	return (nf*sumXY - sumX*sumY) / denom, true
}
