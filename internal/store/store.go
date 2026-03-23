package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	dbPath := filepath.Join(dataDir, "trades.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite handles one writer at a time

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS trades (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			slug            TEXT NOT NULL,
			direction       TEXT NOT NULL,
			token_id        TEXT NOT NULL,
			entry_price     REAL NOT NULL,
			shares          REAL NOT NULL,
			cost            REAL NOT NULL,
			kelly_frac      REAL NOT NULL,
			model_prob      REAL NOT NULL,
			confidence      REAL NOT NULL,
			edge            REAL NOT NULL,
			momentum        REAL,
			imbalance       REAL,
			edge_signal     REAL,
			tradeflow       REAL,
			btc_price       REAL,
			btc_volatility  REAL,
			opened_at       DATETIME NOT NULL,
			won             INTEGER,
			pnl             REAL,
			outcome         TEXT,
			settled_at      DATETIME,
			bankroll_after  REAL
		);

		CREATE TABLE IF NOT EXISTS snapshots (
			id               INTEGER PRIMARY KEY AUTOINCREMENT,
			ts               DATETIME NOT NULL,
			btc_price        REAL,
			btc_trend        TEXT,
			market_slug      TEXT,
			expiry_sec       INTEGER,
			up_bid           REAL,
			up_ask           REAL,
			up_spread        REAL,
			down_bid         REAL,
			down_ask         REAL,
			down_spread      REAL,
			trade_count      INTEGER,
			price_buf_len    INTEGER,
			bankroll         REAL,
			total_pnl        REAL,
			open_positions   INTEGER,
			exposure         REAL,
			wins             INTEGER,
			losses           INTEGER
		);

		CREATE INDEX IF NOT EXISTS idx_trades_slug ON trades(slug);
		CREATE INDEX IF NOT EXISTS idx_trades_opened ON trades(opened_at);
		CREATE INDEX IF NOT EXISTS idx_trades_settled ON trades(settled_at);
		CREATE INDEX IF NOT EXISTS idx_snapshots_ts ON snapshots(ts);
	`)
	if err != nil {
		return err
	}

	// Best-effort migrations for existing databases (columns may already exist)
	s.db.Exec(`ALTER TABLE trades ADD COLUMN tradeflow REAL`)
	s.db.Exec(`ALTER TABLE trades ADD COLUMN btc_volatility REAL`)

	return nil
}

// TradeRecord represents a paper trade for persistence.
type TradeRecord struct {
	Slug          string
	Direction     string
	TokenID       string
	EntryPrice    float64
	Shares        float64
	Cost          float64
	KellyFrac     float64
	ModelProb     float64
	Confidence    float64
	Edge          float64
	Momentum      float64
	Imbalance     float64
	EdgeSignal    float64
	TradeFlow     float64
	BTCPrice      float64
	BTCVolatility float64
	OpenedAt      time.Time
}

func (s *Store) LogTrade(t TradeRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO trades (slug, direction, token_id, entry_price, shares, cost,
			kelly_frac, model_prob, confidence, edge, momentum, imbalance,
			edge_signal, tradeflow, btc_price, btc_volatility, opened_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Slug, t.Direction, t.TokenID, t.EntryPrice, t.Shares, t.Cost,
		t.KellyFrac, t.ModelProb, t.Confidence, t.Edge, t.Momentum, t.Imbalance,
		t.EdgeSignal, t.TradeFlow, t.BTCPrice, t.BTCVolatility, t.OpenedAt,
	)
	return err
}

func (s *Store) SettleTrade(slug string, won bool, pnl float64, outcome string, bankrollAfter float64) error {
	wonInt := 0
	if won {
		wonInt = 1
	}
	_, err := s.db.Exec(`
		UPDATE trades SET won = ?, pnl = ?, outcome = ?, settled_at = ?, bankroll_after = ?
		WHERE slug = ? AND settled_at IS NULL`,
		wonInt, pnl, outcome, time.Now(), bankrollAfter, slug,
	)
	return err
}

// SnapshotRecord captures periodic hub + risk state.
type SnapshotRecord struct {
	BTCPrice      float64
	BTCTrend      string
	MarketSlug    string
	ExpirySec     int
	UpBid         float64
	UpAsk         float64
	UpSpread      float64
	DownBid       float64
	DownAsk       float64
	DownSpread    float64
	TradeCount    int
	PriceBufLen   int
	Bankroll      float64
	TotalPnL      float64
	OpenPositions int
	Exposure      float64
	Wins          int
	Losses        int
}

func (s *Store) LogSnapshot(r SnapshotRecord) error {
	_, err := s.db.Exec(`
		INSERT INTO snapshots (ts, btc_price, btc_trend, market_slug, expiry_sec,
			up_bid, up_ask, up_spread, down_bid, down_ask, down_spread,
			trade_count, price_buf_len, bankroll, total_pnl, open_positions,
			exposure, wins, losses)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		time.Now(), r.BTCPrice, r.BTCTrend, r.MarketSlug, r.ExpirySec,
		r.UpBid, r.UpAsk, r.UpSpread, r.DownBid, r.DownAsk, r.DownSpread,
		r.TradeCount, r.PriceBufLen, r.Bankroll, r.TotalPnL, r.OpenPositions,
		r.Exposure, r.Wins, r.Losses,
	)
	return err
}

// --- Query methods for stats computation ---

type SettledTrade struct {
	Slug          string
	Direction     string
	EntryPrice    float64
	Cost          float64
	ModelProb     float64
	Confidence    float64
	Edge          float64
	Momentum      float64
	Imbalance     float64
	EdgeSignal    float64
	TradeFlow     float64
	BTCVolatility float64
	Won           bool
	PnL           float64
	OpenedAt      time.Time
	SettledAt     time.Time
}

func (s *Store) SettledTradesSince(since time.Time) ([]SettledTrade, error) {
	rows, err := s.db.Query(`
		SELECT slug, direction, entry_price, cost, model_prob, confidence, edge,
			COALESCE(momentum, 0), COALESCE(imbalance, 0), COALESCE(edge_signal, 0),
			COALESCE(tradeflow, 0), COALESCE(btc_volatility, 0),
			won, pnl, opened_at, settled_at
		FROM trades WHERE settled_at IS NOT NULL AND settled_at >= ?
		ORDER BY settled_at`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []SettledTrade
	for rows.Next() {
		var t SettledTrade
		var wonInt int
		if err := rows.Scan(&t.Slug, &t.Direction, &t.EntryPrice, &t.Cost,
			&t.ModelProb, &t.Confidence, &t.Edge, &t.Momentum,
			&t.Imbalance, &t.EdgeSignal, &t.TradeFlow, &t.BTCVolatility,
			&wonInt, &t.PnL, &t.OpenedAt, &t.SettledAt); err != nil {
			return nil, err
		}
		t.Won = wonInt == 1
		trades = append(trades, t)
	}
	return trades, rows.Err()
}

func (s *Store) AllSettledTrades() ([]SettledTrade, error) {
	return s.SettledTradesSince(time.Time{})
}

// --- API query methods ---

// TradeRow represents a trade for API responses (handles unsettled trades).
type TradeRow struct {
	Slug       string    `json:"slug"`
	Direction  string    `json:"direction"`
	EntryPrice float64   `json:"entry_price"`
	Shares     float64   `json:"shares"`
	Cost       float64   `json:"cost"`
	KellyFrac  float64   `json:"kelly_frac"`
	ModelProb  float64   `json:"model_prob"`
	Confidence float64   `json:"confidence"`
	Edge       float64   `json:"edge"`
	BTCPrice   float64   `json:"btc_price"`
	OpenedAt   time.Time `json:"opened_at"`
	Settled    bool      `json:"settled"`
	Won        *bool     `json:"won"`
	PnL        *float64  `json:"pnl"`
	Outcome    string    `json:"outcome,omitempty"`
}

func (s *Store) RecentTrades(limit int) ([]TradeRow, error) {
	rows, err := s.db.Query(`
		SELECT slug, direction, entry_price, shares, cost, kelly_frac, model_prob,
			confidence, edge, COALESCE(btc_price, 0), opened_at,
			COALESCE(won, -1), COALESCE(pnl, 0), COALESCE(outcome, ''),
			settled_at IS NOT NULL
		FROM trades ORDER BY opened_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var trades []TradeRow
	for rows.Next() {
		var t TradeRow
		var wonInt int
		var pnl float64
		if err := rows.Scan(&t.Slug, &t.Direction, &t.EntryPrice, &t.Shares,
			&t.Cost, &t.KellyFrac, &t.ModelProb, &t.Confidence, &t.Edge,
			&t.BTCPrice, &t.OpenedAt, &wonInt, &pnl, &t.Outcome, &t.Settled); err != nil {
			return nil, err
		}
		if t.Settled {
			w := wonInt == 1
			t.Won = &w
			t.PnL = &pnl
		}
		trades = append(trades, t)
	}
	return trades, rows.Err()
}
