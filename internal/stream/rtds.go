package stream

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"go-trading/internal/hub"

	"github.com/gorilla/websocket"
)

const (
	rtdsProdURL     = "wss://ws-live-data.polymarket.com"
	rtdsPingEvery   = 5 * time.Second
	rtdsMinBackoff  = 1 * time.Second
	rtdsMaxBackoff  = 60 * time.Second
	rtdsSubMessage  = `{"action":"subscribe","subscriptions":[{"topic":"crypto_prices","type":"update"}]}`
)

type RTDSStream struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	handler hub.EventHandler
	logger  *slog.Logger
}

type rtdsMessage struct {
	Topic     string          `json:"topic"`
	Type      string          `json:"type"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type cryptoPricePayload struct {
	Symbol    string  `json:"symbol"`
	Timestamp int64   `json:"timestamp"`
	Value     float64 `json:"value"`
}

func NewRTDSStream(handler hub.EventHandler, logger *slog.Logger) (*RTDSStream, error) {
	conn, _, err := websocket.DefaultDialer.Dial(rtdsProdURL, nil)
	if err != nil {
		return nil, err
	}
	return &RTDSStream{conn: conn, handler: handler, logger: logger}, nil
}

func (s *RTDSStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *RTDSStream) StreamBTCPrice(ctx context.Context) error {
	if err := s.conn.WriteMessage(websocket.TextMessage, []byte(rtdsSubMessage)); err != nil {
		return err
	}
	s.logger.Info("subscribed to crypto price feed (filtering for btcusdt)")

	go s.runWithReconnect(ctx)

	return nil
}

// runWithReconnect manages the read/ping loops and reconnects on failure.
func (s *RTDSStream) runWithReconnect(ctx context.Context) {
	backoff := rtdsMinBackoff

	for {
		done := make(chan struct{})

		// Start ping loop — closes done channel when connection fails.
		go s.pingLoop(ctx, done)

		// Read loop blocks until error or ctx cancelled.
		s.readLoop(ctx, done)

		// If context cancelled, we're shutting down.
		if ctx.Err() != nil {
			return
		}

		// Close the broken connection.
		s.mu.Lock()
		if s.conn != nil {
			s.conn.Close()
			s.conn = nil
		}
		s.mu.Unlock()

		s.logger.Warn("rtds disconnected, reconnecting", "backoff", backoff)

		// Wait before reconnecting.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		// Attempt reconnection.
		conn, _, err := websocket.DefaultDialer.Dial(rtdsProdURL, nil)
		if err != nil {
			s.logger.Warn("rtds reconnect failed", "err", err)
			backoff = min(backoff*2, rtdsMaxBackoff)
			continue
		}

		if err := conn.WriteMessage(websocket.TextMessage, []byte(rtdsSubMessage)); err != nil {
			s.logger.Warn("rtds resubscribe failed", "err", err)
			conn.Close()
			backoff = min(backoff*2, rtdsMaxBackoff)
			continue
		}

		s.mu.Lock()
		s.conn = conn
		s.mu.Unlock()

		s.logger.Info("rtds reconnected successfully")
		backoff = rtdsMinBackoff // reset backoff on success
	}
}

func (s *RTDSStream) pingLoop(ctx context.Context, done chan struct{}) {
	ticker := time.NewTicker(rtdsPingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			s.mu.Lock()
			conn := s.conn
			s.mu.Unlock()
			if conn == nil {
				return
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte("PING")); err != nil {
				s.logger.Warn("rtds ping failed", "err", err)
				return
			}
		}
	}
}

func (s *RTDSStream) readLoop(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		s.mu.Lock()
		conn := s.conn
		s.mu.Unlock()
		if conn == nil {
			return
		}

		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("rtds read error", "err", err)
			return
		}

		raw := string(data)
		if raw == "PONG" || raw == "" {
			continue
		}

		var msg rtdsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg.Topic != "crypto_prices" || msg.Type != "update" {
			continue
		}

		var price cryptoPricePayload
		if err := json.Unmarshal(msg.Payload, &price); err != nil {
			continue
		}

		if price.Symbol != "btcusdt" {
			continue
		}

		s.handler.OnBTCPrice(price.Value, price.Timestamp)
		s.logger.Debug("btc_price", "price", price.Value, "timestamp", price.Timestamp)
	}
}
