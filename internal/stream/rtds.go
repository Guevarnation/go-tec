package stream

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"go-tec/internal/hub"

	"github.com/gorilla/websocket"
)

const (
	rtdsProdURL   = "wss://ws-live-data.polymarket.com"
	rtdsPingEvery = 5 * time.Second
)

type RTDSStream struct {
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
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *RTDSStream) StreamBTCPrice(ctx context.Context) error {
	sub := `{"action":"subscribe","subscriptions":[{"topic":"crypto_prices","type":"update"}]}`
	if err := s.conn.WriteMessage(websocket.TextMessage, []byte(sub)); err != nil {
		return err
	}
	s.logger.Info("subscribed to crypto price feed (filtering for btcusdt)")

	go s.pingLoop(ctx)
	go s.readLoop(ctx)

	return nil
}

func (s *RTDSStream) pingLoop(ctx context.Context) {
	ticker := time.NewTicker(rtdsPingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.conn.WriteMessage(websocket.TextMessage, []byte("PING")); err != nil {
				s.logger.Warn("rtds ping failed", "err", err)
				return
			}
		}
	}
}

func (s *RTDSStream) readLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		_, data, err := s.conn.ReadMessage()
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
