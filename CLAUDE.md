# CLAUDE.md

## Project overview

Go trading bot for Polymarket's BTC Up/Down 5-minute binary prediction markets.
Paper trading only (no real money). Targets EC2 t4g.nano deployment (~$4/mo).

## Build & run

```bash
# Local development (colored logs)
go build -o bot ./cmd/bot && ./bot

# Production build (ARM64 for EC2 Graviton)
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot

# Run tests
go test ./...
```

## Key architecture decisions

- **Pure Go, zero CGo**: Uses `modernc.org/sqlite` for cross-compilation
- **Single binary**: All config via env vars, no config files needed
- **Hub pattern**: Central state store with RWMutex, streams write / signals read
- **Ring buffers**: Fixed-capacity for price and trade history (no unbounded growth)
- **Paper trading**: Risk manager tracks virtual $100 bankroll with Kelly sizing

## Package layout

- `cmd/bot/` - entrypoint, wires 4 background goroutines
- `internal/hub/` - central state: orderbook, price buffer, trade buffer, market state
- `internal/signal/` - 4 evaluators (momentum, imbalance, edge, tradeflow) + engine with volatility gate
- `internal/risk/` - Kelly criterion, position sizing, drawdown breaker
- `internal/store/` - SQLite schema, trade/snapshot persistence
- `internal/stats/` - analytics: Sharpe, Brier, calibration, streaks, edge buckets, signal win rates
- `internal/stream/` - CLOB + RTDS WebSocket clients
- `internal/market/` - Gamma API discovery
- `internal/notify/` - SNS alerts via AWS CLI exec
- `internal/api/` - HTTP status API (health, status, trades, stats)
- `internal/config/` - env-based config

## Conventions

- All env vars have sensible defaults (see `.env.example`)
- JSON logging in production (`LOG_FORMAT=json`), colored text locally
- SQLite WAL mode, single writer connection
- Signal values always in [-1, +1] range
- Kelly fraction always applied as quarter-Kelly (0.25x)

## EC2 deployment

- **Instance**: `i-0924d2b608a3981b4` (go-bot), t4g.nano, us-east-1
- **IP**: `184.72.148.3`
- **IAM role**: `go-trading-bot-role`
- **SSH key**: `/Users/guevara/Desktop/micontax/guevara-key-pair.pem`
- **SNS topic**: `arn:aws:sns:us-east-1:021363511692:go-trading-alerts`
- **S3 backup**: `s3://go-trading-db-backups/backups/`
- **AWS profile**: `personal` (account `021363511692`)

## Common tasks

```bash
# SSH into EC2
ssh -i /Users/guevara/Desktop/micontax/guevara-key-pair.pem ec2-user@184.72.148.3

# View logs on EC2
sudo journalctl -u go-tec -f

# Check API
curl http://184.72.148.3:8080/health
curl http://184.72.148.3:8080/status
curl http://184.72.148.3:8080/trades
curl http://184.72.148.3:8080/stats

# Download trade database
scp -i /Users/guevara/Desktop/micontax/guevara-key-pair.pem \
  ec2-user@184.72.148.3:/opt/go-tec/data/trades.db .

# Query trades locally
sqlite3 trades.db "SELECT * FROM trades ORDER BY opened_at DESC LIMIT 20"

# Deploy update
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot
scp -i /Users/guevara/Desktop/micontax/guevara-key-pair.pem bot ec2-user@184.72.148.3:~/bot
ssh -i /Users/guevara/Desktop/micontax/guevara-key-pair.pem ec2-user@184.72.148.3 \
  "sudo cp ~/bot /opt/go-tec/bot && sudo systemctl restart go-tec"
```

## Polymarket API notes

- No testnet exists; all endpoints are production
- Public WS/REST endpoints require no auth
- Gamma `/events?slug=...` for market discovery (not `/markets` due to SDK bug)
- RTDS WS: subscribe to all crypto prices, filter `btcusdt` client-side
- CLOB WS: SDK handles reconnection and heartbeat

