#!/usr/bin/env bash
set -euo pipefail

# EC2 deployment script for the Polymarket BTC 5-min Paper Trading Bot.
# Run on a fresh Amazon Linux 2023 (ARM64 / Graviton) t4g.nano instance.
#
# Usage:
#   1. Build locally:  GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot
#   2. Copy to EC2:    scp bot deploy/go-tec.service ec2-user@<ip>:~/
#   3. SSH in and run: bash setup.sh

BOT_DIR="/opt/go-tec"
DATA_DIR="${BOT_DIR}/data"
SERVICE_FILE="/etc/systemd/system/go-tec.service"
S3_BUCKET="${GO_TEC_S3_BUCKET:-}"

echo "==> Creating bot directory"
sudo mkdir -p "${BOT_DIR}" "${DATA_DIR}"
sudo chown -R ec2-user:ec2-user "${BOT_DIR}"

echo "==> Installing binary"
cp ~/bot "${BOT_DIR}/bot"
chmod +x "${BOT_DIR}/bot"

echo "==> Installing systemd service"
sudo cp ~/go-tec.service "${SERVICE_FILE}"
sudo systemctl daemon-reload
sudo systemctl enable go-tec
sudo systemctl start go-tec

echo "==> Bot is running. Check status:"
echo "    sudo systemctl status go-tec"
echo "    sudo journalctl -u go-tec -f"

if [ -n "${S3_BUCKET}" ]; then
    echo "==> Setting up daily S3 backup"
    CRON_CMD="0 4 * * * aws s3 cp ${DATA_DIR}/trades.db s3://${S3_BUCKET}/backups/trades-\$(date +\\%Y\\%m\\%d).db"
    (crontab -l 2>/dev/null || true; echo "${CRON_CMD}") | sort -u | crontab -
    echo "    Backup cron installed: daily at 04:00 UTC to s3://${S3_BUCKET}/backups/"
fi

echo "==> Done!"
