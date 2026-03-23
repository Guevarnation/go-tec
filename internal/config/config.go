package config

import (
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	LogLevel         slog.Level
	LogFormat        string // "text" (colored) or "json"
	GammaAPIURL      string
	CLOBWSURL        string
	RTDSWSURL        string
	MarketSlugPrefix string
	DataDir          string // directory for SQLite database
}

func Load() *Config {
	return &Config{
		LogLevel:         parseLogLevel(getEnv("LOG_LEVEL", "INFO")),
		LogFormat:        strings.ToLower(getEnv("LOG_FORMAT", "text")),
		GammaAPIURL:      getEnv("GAMMA_API_URL", "https://gamma-api.polymarket.com"),
		CLOBWSURL:        getEnv("CLOB_WS_URL", "wss://ws-subscriptions-clob.polymarket.com/ws/market"),
		RTDSWSURL:        getEnv("RTDS_WS_URL", "wss://ws-live-data.polymarket.com"),
		MarketSlugPrefix: getEnv("MARKET_SLUG_PREFIX", "btc-updown-5m"),
		DataDir:          getEnv("DATA_DIR", "./data"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToUpper(s) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
