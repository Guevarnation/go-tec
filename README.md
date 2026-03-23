ssh -i /Users/guevara/Desktop/micontax/guevara-key-pair.pem ec2-user@184.72.148.3

# Polymarket BTC 5-Min Paper Trading Bot

A Go bot that paper-trades Polymarket's Bitcoin Up/Down 5-minute prediction
markets. Streams real-time data, evaluates signals, sizes positions via Kelly
Criterion, and logs everything to SQLite for analysis.

## Quick Start

```bash
# Run locally with colored output
go run ./cmd/bot

# Debug logging
LOG_LEVEL=DEBUG go run ./cmd/bot

# JSON output (for production / CloudWatch)
LOG_FORMAT=json go run ./cmd/bot
```

No API keys, no wallet, no authentication needed. The bot reads public data
and paper-trades with a $100 simulated bankroll.

## What It Does

1. Discovers BTC 5-minute Up/Down markets via the Gamma API
2. Streams real-time orderbook, trades, and BTC price via WebSocket
3. Evaluates signals every 5s (momentum, orderbook imbalance, edge)
4. Sizes paper trades using Kelly Criterion with risk limits
5. Settles positions when markets resolve (WS events + API fallback)
6. Logs every trade and settlement to `data/trades.db` (SQLite)
7. Computes hourly performance stats: win rate, Sharpe, calibration, P&L by hour

## Deploy to EC2

```bash
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot
scp bot deploy/go-trading.service deploy/setup.sh ec2-user@<ip>:~/
ssh ec2-user@<ip> "bash setup.sh"
```

See [deploy/EC2-SETUP.md](deploy/EC2-SETUP.md) for full instructions.
Estimated cost: ~$3.72/month on t4g.nano.

## Analyze Results

```bash
# Download the trade database
scp ec2-user@<ip>:/opt/go-tec/data/trades.db ./trades.db

# Win rate and P&L
sqlite3 trades.db "SELECT COUNT(*), SUM(CASE WHEN won=1 THEN 1 ELSE 0 END), ROUND(SUM(pnl),2) FROM trades WHERE settled_at IS NOT NULL;"
```

## Architecture

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for full documentation.

## Configuration

| Variable             | Default         | Description                                   |
| -------------------- | --------------- | --------------------------------------------- |
| `LOG_LEVEL`          | `INFO`          | DEBUG, INFO, WARN, ERROR                      |
| `LOG_FORMAT`         | `text`          | `text` (colored) or `json` (machine-readable) |
| `DATA_DIR`           | `./data`        | Directory for SQLite database                 |
| `MARKET_SLUG_PREFIX` | `btc-updown-5m` | Market slug prefix                            |

## Requirements

- Go 1.25+
