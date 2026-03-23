# Architecture

## Overview

A Go trading bot for Polymarket's Bitcoin Up/Down 5-minute prediction markets.
Each market is a binary bet: will BTC's price (per Chainlink BTC/USD) finish
higher or lower than its opening price over a specific 5-minute window? Correct
shares pay $1, incorrect pay $0.

## Project Structure

```
go-tec/
  cmd/bot/main.go                 entrypoint, wires everything together
  internal/
    config/config.go              env-based configuration
    market/discovery.go           Gamma API: find active BTC 5m markets
    hub/
      handler.go                  EventHandler interface (stream -> hub contract)
      hub.go                      central state store, implements EventHandler
      orderbook.go                per-asset sorted orderbook with query methods
      pricebuf.go                 BTC price ring buffer (SMA, slope, latest)
      market_state.go             market lifecycle tracking (trading -> resolved)
      metrics.go                  trade buffer, VWAP, velocity calculations
    stream/
      clob_ws.go                  CLOB WebSocket (orderbook, trades, events)
      rtds.go                     RTDS WebSocket (live BTC/USD price)
    signal/
      signal.go                 core types (Score, Decision, Direction, Evaluator)
      momentum.go               BTC price momentum via slope
      imbalance.go              orderbook bid/ask depth imbalance
      edge.go                   market mispricing detector
      engine.go                 composite engine with weighted signals
    risk/
      kelly.go                  Kelly Criterion for binary outcome bets
      manager.go                position sizing, paper P&L, drawdown breaker
    store/
      store.go                  SQLite persistence: trades, settlements, snapshots
    stats/
      stats.go                  performance analytics: win rate, Sharpe, calibration
    execution/                    (Phase 5) order placement
  deploy/
    go-tec.service              systemd unit file
    setup.sh                    EC2 setup and deployment script
    EC2-SETUP.md                full deployment guide
  Dockerfile                    multi-stage build for ARM64
  docs/                           this documentation
  .env.example                    configuration reference
```

## Polymarket API Landscape

Polymarket exposes four APIs. No testnet exists; all endpoints are production.
Public (read-only) endpoints require no authentication and cost nothing.

| API            | Base URL                                               | Auth             | Purpose                                 |
| -------------- | ------------------------------------------------------ | ---------------- | --------------------------------------- |
| Gamma API      | `https://gamma-api.polymarket.com`                     | None             | Market discovery, metadata, search      |
| CLOB REST      | `https://clob.polymarket.com`                          | L1/L2 for writes | Prices, orderbook, order placement      |
| CLOB WebSocket | `wss://ws-subscriptions-clob.polymarket.com/ws/market` | None (public)    | Real-time orderbook, trades, events     |
| RTDS WebSocket | `wss://ws-live-data.polymarket.com`                    | None             | Live crypto prices (Binance, Chainlink) |

## How BTC 5-Minute Markets Work

- New markets are created every 5 minutes, 24/7
- Event slugs follow the pattern `btc-updown-5m-{unix_timestamp}` where the
  timestamp is the window start time, rounded to 300-second boundaries
- Each event contains one market with two outcomes: "Up" and "Down"
- Each outcome has a CLOB token ID used for WebSocket subscriptions and trading
- Resolution uses Chainlink BTC/USD: if closing price >= opening price, "Up" wins

### Market Discovery Strategy

We do NOT use the Gamma `/markets` endpoint because:

1. The SDK's `gamma.Market` struct has a type mismatch bug (`volumeNum` is
   declared as `string` but the API returns a number), causing deserialization
   failures
2. The 5-min markets are best discovered via the `/events` endpoint using the
   predictable slug pattern

Instead, we compute the expected slugs from the current time:

```
baseTS = floor(now_unix / 300) * 300
slugs  = btc-updown-5m-{baseTS}, btc-updown-5m-{baseTS+300}, ...
```

Then fetch each from `GET /events?slug={slug}` using raw `net/http`.

## Data Streams

### CLOB WebSocket (via Go SDK)

Uses `github.com/GoPolymarket/polymarket-go-sdk/pkg/clob/ws`. The SDK handles:

- Automatic WebSocket reconnection
- Heartbeat (PING/PONG every 10 seconds)
- Typed Go channels for each event type

We subscribe to these event types for the discovered asset IDs:

| Event Type         | Channel Return Type          | Data                                     |
| ------------------ | ---------------------------- | ---------------------------------------- |
| `orderbook`        | `<-chan OrderbookEvent`      | Full L2 book (bids/asks with price+size) |
| `last_trade_price` | `<-chan LastTradePriceEvent` | Trade executions (price, size, side)     |
| `best_bid_ask`     | `<-chan BestBidAskEvent`     | Top-of-book with spread                  |
| `new_market`       | `<-chan NewMarketEvent`      | New market creation                      |
| `market_resolved`  | `<-chan MarketResolvedEvent` | Resolution with winning outcome          |

### RTDS WebSocket (via gorilla/websocket)

Uses `github.com/gorilla/websocket` directly instead of the SDK's RTDS client.

**Why not the SDK?** Two issues:

1. `rtds.NewClient("")` starts connection asynchronously. `SubscribeCryptoPrices()`
   blocks on an internal `connReady` channel that never signals, hanging the bot.
2. The SDK sends `filters` as a JSON array (`["btcusdt"]`), but the RTDS server
   rejects any `filters` value for the `crypto_prices` topic with a 400 error.

**Working approach:** Subscribe to all crypto prices (no filters), filter for
`btcusdt` client-side. The server sends updates for ~6 symbols every second,
so the overhead is negligible.

Protocol details:

- Send `PING` text message every 5 seconds (server responds with `PONG`)
- Subscribe: `{"action":"subscribe","subscriptions":[{"topic":"crypto_prices","type":"update"}]}`
- Messages: `{"topic":"crypto_prices","type":"update","timestamp":...,
"payload":{"symbol":"btcusdt","value":68127.41,"timestamp":...}}`

## Configuration

All configuration via environment variables with sensible defaults:

| Variable             | Default                                                | Description                                            |
| -------------------- | ------------------------------------------------------ | ------------------------------------------------------ |
| `LOG_LEVEL`          | `INFO`                                                 | DEBUG, INFO, WARN, ERROR                               |
| `LOG_FORMAT`         | `text`                                                 | `text` (colored terminal) or `json` (machine-readable) |
| `GAMMA_API_URL`      | `https://gamma-api.polymarket.com`                     | Gamma API base URL                                     |
| `CLOB_WS_URL`        | `wss://ws-subscriptions-clob.polymarket.com/ws/market` | CLOB WS endpoint                                       |
| `RTDS_WS_URL`        | `wss://ws-live-data.polymarket.com`                    | RTDS WS endpoint                                       |
| `MARKET_SLUG_PREFIX` | `btc-updown-5m`                                        | Market slug prefix for discovery                       |
| `DATA_DIR`           | `./data`                                               | Directory for SQLite trade database                    |

## Dependencies

| Library                          | Version | Purpose                                               |
| -------------------------------- | ------- | ----------------------------------------------------- |
| `GoPolymarket/polymarket-go-sdk` | v1.1.0  | CLOB WebSocket client (orderbook, trades, events)     |
| `gorilla/websocket`              | v1.5.3  | RTDS WebSocket (pulled in by SDK, also used directly) |
| `charmbracelet/log`              | v1.0.0  | Colored structured logging for development            |
| `modernc.org/sqlite`             | v1.47.0 | Pure-Go SQLite driver (no CGo, cross-compile friendly)|
| `log/slog` (stdlib)              | Go 1.25 | JSON structured logging for production                |

## Hub (Phase 2) -- Central State Store

The Hub is the bridge between "raw data" and "trading decisions". All WebSocket
events flow into the Hub via the `EventHandler` interface. Signal models (Phase 3)
query the Hub for derived metrics.

### Data flow

```
CLOB WS → stream layer → EventHandler.OnOrderbook/OnTrade/... → Hub state
RTDS WS → stream layer → EventHandler.OnBTCPrice                → Hub state
Signal models ← Hub.BTCPriceSlope(), Hub.ImpliedProbability(), etc.
```

### EventHandler interface

Defined in `hub/handler.go`. The stream layer parses raw SDK string values into
typed Go values and calls these methods:

- `OnOrderbook(assetID, bids, asks, ts)` -- full L2 book snapshot
- `OnTrade(assetID, price, size, side, ts)` -- trade execution
- `OnBTCPrice(price, ts)` -- BTC/USDT price tick
- `OnBestBidAsk(assetID, bid, ask, spread, ts)` -- top-of-book update
- `OnMarketResolved(id, slug, outcome, winnerID, ts)` -- market settled
- `OnNewMarket(id, slug, question, assetIDs, ts)` -- new market created

### State components

| Component     | Backing structure               | Purpose                                              |
| ------------- | ------------------------------- | ---------------------------------------------------- |
| `Orderbook`   | Sorted `[]OrderLevel` per asset | Best bid/ask, mid-price, spread, depth, imbalance    |
| `PriceBuffer` | Ring buffer (cap=600)           | BTC price history, SMA, linear regression slope      |
| `MarketState` | `map[slug]*MarketState`         | Market lifecycle, time-to-expiry, resolution outcome |
| `TradeBuffer` | Ring buffer (cap=200) per asset | VWAP, trade velocity                                 |

### Concurrency model

`sync.RWMutex` -- write lock in `On*` methods (single writer per event type),
read lock in query methods (multiple concurrent readers for signal models).

### Market rotation

A background goroutine runs every 60 seconds:

1. Computes expected slugs for the next 3 windows
2. Queries Gamma API for each
3. Registers new markets in the Hub
4. Subscribes to their asset IDs on the CLOB WebSocket

This makes the bot run indefinitely instead of dying after 20 minutes.

### Periodic snapshot

Every 30 seconds, the bot logs a one-line summary replacing the per-event firehose:

```
btc=$87234.50 trend=rising market=btc-updown-5m-1742691600 expiry=3m12s
up=[0.5500/0.5600 spread=0.0100] down=[0.4400/0.4500 spread=0.0100]
trades_60s=14 buf=183
```

## Lifecycle

1. Load configuration from environment
2. Initialize logger (colored text or JSON based on `LOG_FORMAT`)
3. Open SQLite trade database (`DATA_DIR/trades.db`)
4. Create Hub (central state store)
5. Discover current + next 3 BTC 5-min market windows via Gamma API
6. Register discovered markets in the Hub
7. Connect CLOB WebSocket, subscribe to all token IDs (events flow to Hub)
8. Connect RTDS WebSocket, subscribe to crypto prices (BTC prices flow to Hub)
9. Initialize signal engine and risk manager ($100 paper bankroll)
10. Start background goroutines (see below)
11. Block on `ctx.Done()` (SIGINT/SIGTERM triggers graceful shutdown)
12. Close WebSocket connections and SQLite database, exit

### Background goroutines

| Goroutine | Interval | Purpose |
|-----------|----------|---------|
| `marketRotation` | 60s | Discover new 5-min windows, subscribe to their assets, settle expired positions via Gamma API fallback |
| `snapshotLoop` | 30s | Log hub + risk state summary, persist snapshot to SQLite |
| `signalLoop` | 5s | Evaluate signals, settle resolved positions, open paper trades via risk manager, persist trades to SQLite |
| `hourlyStatsLoop` | 1h | Compute and log all-time + last-hour summary, calibration buckets, P&L by hour |

## Signal Engine (Phase 3)

The signal engine evaluates every 5 seconds whether to buy Up or Down.
It combines three independent signals into a composite trading decision.

### Signals

| Signal    | Weight | Input                          | Logic                                                                    |
| --------- | ------ | ------------------------------ | ------------------------------------------------------------------------ |
| Momentum  | 0.50   | `BTCPriceSlope(60)`            | BTC rising → Up more likely. Uses `tanh(slope * 2.0)` to map to [-1, +1] |
| Imbalance | 0.20   | `Orderbook.BidAskImbalance(5)` | More bid depth on Up token → market is bullish                           |
| Edge      | 0.30   | `sigmoid(slope) - midPrice`    | Divergence between model-predicted prob and market-implied prob          |

### Composite decision

```
composite = sum(signal_i * weight_i) / sum(weight_i)
confidence = abs(composite)            →  0.0 to 1.0
direction  = sign(composite)           →  BUY_UP or BUY_DOWN
edge       = abs(model_prob - market_prob)
```

### Trading gates

The engine only recommends trading when ALL conditions are met:

- Time window: 30s < time_to_expiry < 4m30s (need data, need fills)
- Data: at least 30 BTC price ticks collected
- Confidence > 0.15 (composite signal strength)
- Edge > 0.03 (sufficient model-vs-market divergence)

Decisions that pass all gates are forwarded to the risk manager for Kelly sizing
and paper trade execution.

### Files

```
internal/signal/
  signal.go      types: Score, Decision, Direction, Evaluator interface
  momentum.go    BTC price momentum via linear regression slope
  imbalance.go   orderbook bid/ask depth imbalance
  edge.go        market mispricing detector (sigmoid momentum vs implied prob)
  engine.go      composite engine: weighted combination, gates, decision output
```

## Risk Manager (Phase 4) -- Paper Trading & Position Sizing

The risk manager sits between the signal engine and (future) execution engine.
It sizes positions using Kelly Criterion, enforces risk limits, and tracks
paper P&L including win/loss records.

### Kelly Criterion for binary markets

Polymarket shares pay $1 if the outcome wins, $0 if it loses.
The optimal bet fraction is:

```
f* = (p - cost) / (1 - cost)
```

where `p` = model probability and `cost` = entry price (best ask).
We use quarter Kelly (`f* * 0.25`) to be conservative.

### Risk limits

| Limit | Default | Purpose |
|-------|---------|---------|
| Fractional Kelly | 0.25 | Bet 25% of full Kelly -- reduces variance |
| Max position | $10 | Cap per single market |
| Max exposure | $25 | Cap across all open positions |
| Drawdown limit | 20% | Halt trading if bankroll drops 20% from peak |
| Min bet | $0.50 | Avoid dust positions |

### Position lifecycle

1. Signal engine produces `Decision{ShouldTrade: true, ModelProb: 0.83}`
2. Risk manager looks up best ask price for the target token
3. Kelly sizes the bet: `f* = (0.83 - 0.77) / (1 - 0.77) = 0.26`, quarter Kelly = 6.5% of bankroll
4. Exposure and position caps applied
5. Paper position opened and logged
6. When market resolves (via WS event or Gamma API fallback), P&L settled

### Resolution detection

Two mechanisms ensure positions always settle:
- **WebSocket**: Hub's `OnMarketResolved` (primary, matched by slug or winning asset ID)
- **Gamma API fallback**: Every 60s, expired positions are checked via `Discovery.CheckResolution` -- if the event is closed on the API, the hub is force-resolved and the position settles on the next tick

### Files

```
internal/risk/
  kelly.go       Kelly Criterion math for binary outcome markets
  manager.go     position tracking, sizing, settlement, drawdown breaker, P&L
```

## Trade Database (Phase 6) -- SQLite Persistence & Analytics

Every paper trade, settlement, and periodic snapshot is persisted to a SQLite
database (`data/trades.db`). This enables post-hoc analysis after running the
bot 24/7 on EC2.

### Schema

Two tables:

- **trades**: One row per paper trade. Records entry details (price, cost, Kelly,
  model prob, all signal values, BTC price). Settlement fields (won, pnl, outcome,
  bankroll_after) are NULL until resolved, then filled via `UPDATE`.
- **snapshots**: Periodic (30s) hub + risk state: BTC price, trend, market slug,
  orderbook quotes, bankroll, exposure, win/loss record.

### Analytics computed every hour

| Metric | What it tells you |
|--------|-------------------|
| Win rate | Basic profitability signal |
| Profit factor | Gross wins / gross losses (>1.0 = profitable) |
| Sharpe ratio | Risk-adjusted return, annualized |
| Max drawdown | Worst peak-to-trough loss |
| Calibration | When model says 70%, does Up actually win ~70%? |
| P&L by hour | Which hours of the day are most/least profitable |

### Key queries

The database can be downloaded via `scp` and queried with any SQLite client.
See `deploy/EC2-SETUP.md` for ready-made SQL queries.

### Files

```
internal/store/
  store.go       schema migration, LogTrade, SettleTrade, LogSnapshot, query methods
internal/stats/
  stats.go       ComputeSummary, ComputeCalibration, ComputeHourlyPnL, Sharpe ratio
```

## EC2 Deployment (Phase 7)

Target: EC2 `t4g.nano` (ARM Graviton, 512MB RAM, ~$3/mo with savings plan).

### Why not Fargate/ECS?

- Bot is a single long-running process with persistent WebSocket connections
- Fargate cold starts (10-30s) risk missing 5-min market windows
- ECS agent consumes ~200MB on a 512MB instance (40% waste)
- Fargate costs ~$9-12/mo vs ~$3-4/mo for equivalent EC2

### Build and deploy

```
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot
scp bot deploy/go-tec.service deploy/setup.sh ec2-user@<ip>:~/
ssh ec2-user@<ip> "bash setup.sh"
```

### Runtime

- **Process management**: systemd with `Restart=always`
- **Storage**: SQLite on EBS (`/opt/go-tec/data/trades.db`)
- **Backups**: Optional daily cron to S3
- **Logs**: `journalctl -u go-tec -f` (JSON format in production)

### Files

```
Dockerfile                       multi-stage ARM64 build (distroless base)
deploy/
  go-tec.service                 systemd unit
  setup.sh                       EC2 setup automation
  EC2-SETUP.md                   full deployment walkthrough
```

### Cost estimate

| Resource | Monthly |
|----------|---------|
| t4g.nano on-demand | ~$3.07 |
| 8 GB gp3 EBS | ~$0.64 |
| S3 backups | ~$0.01 |
| **Total** | **~$3.72** |

## Full Data Flow

```
                          ┌──────────────┐
                          │  Gamma API   │
                          │  (REST)      │
                          └──────┬───────┘
                                 │ market discovery (60s)
                                 │ resolution fallback (60s)
                                 ▼
┌──────────────┐         ┌──────────────┐         ┌──────────────┐
│  CLOB WS     │────────>│     Hub      │<────────│  RTDS WS     │
│  orderbook   │ OnOrder │  (state)     │OnBTC    │  btc price   │
│  trades      │ OnTrade │              │ Price   │              │
│  events      │ OnRes   │              │         │              │
└──────────────┘         └──────┬───────┘         └──────────────┘
                                │ query methods
                                ▼
                         ┌──────────────┐
                         │ Signal Engine│
                         │ momentum     │
                         │ imbalance    │
                         │ edge         │
                         └──────┬───────┘
                                │ Decision
                                ▼
                         ┌──────────────┐
                         │ Risk Manager │
                         │ Kelly sizing │
                         │ paper P&L    │
                         └──────┬───────┘
                                │ Trade / Settlement
                                ▼
                         ┌──────────────┐
                         │   SQLite     │
                         │  trades.db   │◄──── hourly stats
                         └──────────────┘
```

## Known Limitations & Future Improvements

- **Signal naivete**: The momentum/imbalance/edge signals are a starting point.
  After collecting a week of data, analyze calibration and P&L by hour to identify
  where the model is weak. Possible improvements: volatility regime filter, VWAP
  divergence signal, multi-timeframe momentum.
- **No real execution**: Phase 5 (wallet auth, CLOB order placement) is intentionally
  deferred until paper trading demonstrates a consistent edge over 1,000+ markets.
- **Single-market focus**: Currently trades only BTC 5-min. The architecture supports
  other Polymarket markets with minimal changes to the discovery layer.
- **No WebSocket reconnection handling**: If the RTDS or CLOB WS drops, the bot
  currently loses data until restart. Adding automatic reconnection with backoff
  would improve uptime.
- **Backtest from SQLite**: The snapshots table contains enough data to replay
  historical market conditions and test new signal parameters offline.

## Phases

| Phase | Status   | Description                                                                  |
| ----- | -------- | ---------------------------------------------------------------------------- |
| 1     | COMPLETE | Connect to WebSocket APIs, stream real-time data                             |
| 2     | COMPLETE | Hub, in-memory orderbook, BTC price buffer, market rotation, derived metrics |
| 3     | COMPLETE | Signal engine: momentum, imbalance, edge, composite decision                 |
| 4     | COMPLETE | Risk model: Kelly sizing, paper P&L, exposure limits, drawdown breaker       |
| 5     | SKIPPED  | Execution engine -- deferred until paper trading proves edge                 |
| 6     | COMPLETE | SQLite trade log, performance analytics, hourly stats                        |
| 7     | COMPLETE | EC2 deployment: Dockerfile, systemd, setup script, S3 backup                |
