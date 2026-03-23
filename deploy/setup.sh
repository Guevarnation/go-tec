#!/usr/bin/env bash
set -euo pipefail

# EC2 deployment script for the Polymarket BTC 5-min Paper Trading Bot.
# Run on a fresh Amazon Linux 2023 (ARM64 / Graviton) t4g.nano instance.
#
# Usage:
#   1. Build locally:  GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot
#   2. Copy to EC2:    scp bot deploy/go-trading.service ec2-user@<ip>:~/
#   3. SSH in and run: bash setup.sh

BOT_DIR="/opt/go-trading"
DATA_DIR="${BOT_DIR}/data"
SERVICE_FILE="/etc/systemd/system/go-trading.service"
ALERT_SERVICE_FILE="/etc/systemd/system/go-trading-alert.service"
S3_BUCKET="${GO_TEC_S3_BUCKET:-}"
SNS_TOPIC="${SNS_TOPIC_ARN:-}"

echo "==> Creating bot directory"
sudo mkdir -p "${BOT_DIR}" "${DATA_DIR}"
sudo chown -R ec2-user:ec2-user "${BOT_DIR}"

echo "==> Installing binary"
cp ~/bot "${BOT_DIR}/bot"
chmod +x "${BOT_DIR}/bot"

# Write .env file for SNS_TOPIC_ARN (used by both the bot and the alert service)
if [ -n "${SNS_TOPIC}" ]; then
    echo "SNS_TOPIC_ARN=${SNS_TOPIC}" > "${BOT_DIR}/.env"
    echo "==> SNS topic configured: ${SNS_TOPIC}"
fi

echo "==> Installing systemd services"
sudo cp ~/go-trading.service "${SERVICE_FILE}"
sudo cp ~/go-trading-alert.service "${ALERT_SERVICE_FILE}"
sudo systemctl daemon-reload
sudo systemctl enable go-trading
sudo systemctl start go-trading

echo "==> Bot is running. Check status:"
echo "    sudo systemctl status go-trading"
echo "    sudo journalctl -u go-trading -f"

if [ -n "${S3_BUCKET}" ]; then
    echo "==> Setting up daily S3 backup"
    CRON_CMD="0 4 * * * aws s3 cp ${DATA_DIR}/trades.db s3://${S3_BUCKET}/backups/trades-\$(date +\\%Y\\%m\\%d).db"
    (crontab -l 2>/dev/null || true; echo "${CRON_CMD}") | sort -u | crontab -
    echo "    Backup cron installed: daily at 04:00 UTC to s3://${S3_BUCKET}/backups/"
fi

echo "==> Done!"
