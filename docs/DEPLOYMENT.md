# EC2 Deployment Guide

## Infrastructure

| Resource | Value |
| --- | --- |
| Account | `021363511692` (AWS profile: `personal`) |
| Region | `us-east-1` (N. Virginia) |
| Instance | `i-0924d2b608a3981b4` (go-bot), t4g.nano |
| Public IP | `184.72.148.3` |
| AMI | Amazon Linux 2023 ARM64 (`ami-0b11e0ed3f8697f97`) |
| IAM role | `go-trading-bot-role` |
| SSH key | `guevara-key-pair` |
| EBS | gp3 8GB, DeleteOnTermination=No |
| Security group | SSH (22) + TCP (8080) from your IP only |
| SNS topic | `arn:aws:sns:us-east-1:021363511692:go-trading-alerts` |
| S3 bucket | `go-trading-db-backups` |

## Architecture

```
Your machine ──SSH/SCP──► EC2 t4g.nano (public subnet, default VPC)
                            │
                            ├── systemd (go-tec.service)
                            │     └── /opt/go-tec/bot (single binary)
                            │
                            ├── SQLite (/opt/go-tec/data/trades.db)
                            │     └── daily backup → S3 at 04:00 UTC
                            │
                            ├── HTTP API (:8080) ← your IP only
                            │
                            └── SNS alerts → your email
```

No Docker, no NAT Gateway, no ALB, no RDS. Single binary managed by systemd.

## IAM Role Permissions

`go-trading-bot-role` has a custom inline policy with least-privilege:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "SNSPublishAlerts",
      "Effect": "Allow",
      "Action": "sns:Publish",
      "Resource": "arn:aws:sns:us-east-1:021363511692:go-trading-alerts"
    },
    {
      "Sid": "S3BackupDB",
      "Effect": "Allow",
      "Action": "s3:PutObject",
      "Resource": "arn:aws:s3:::go-trading-db-backups/backups/*"
    }
  ]
}
```

## Security Group Rules

| Direction | Type | Port | Source |
| --- | --- | --- | --- |
| Inbound | SSH | 22 | Your IP (`x.x.x.x/32`) |
| Inbound | Custom TCP | 8080 | Your IP (`x.x.x.x/32`) |
| Outbound | All traffic | All | `0.0.0.0/0` |

Update your IP if it changes: EC2 console → Security Groups → Edit inbound rules.

## Initial Setup (already done)

```bash
# 1. Build
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot

# 2. Copy to EC2
scp -i ~/Desktop/micontax/guevara-key-pair.pem \
  bot deploy/go-tec.service deploy/go-tec-alert.service deploy/setup.sh \
  ec2-user@184.72.148.3:~/

# 3. SSH in and run setup
ssh -i ~/Desktop/micontax/guevara-key-pair.pem ec2-user@184.72.148.3
SNS_TOPIC_ARN=arn:aws:sns:us-east-1:021363511692:go-trading-alerts \
GO_TEC_S3_BUCKET=go-trading-db-backups \
bash ~/setup.sh

# 4. Install cron (Amazon Linux 2023 doesn't include it)
sudo dnf install -y cronie
sudo systemctl enable crond --now
(echo "0 4 * * * aws s3 cp /opt/go-tec/data/trades.db s3://go-trading-db-backups/backups/trades-\$(date +\%Y\%m\%d).db") | crontab -

# 5. Enable API
echo "API_PORT=8080" >> /opt/go-tec/.env
sudo systemctl restart go-tec
```

## Deploy Updates

```bash
# Build new binary
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot

# Copy and restart
scp -i ~/Desktop/micontax/guevara-key-pair.pem bot ec2-user@184.72.148.3:~/bot
ssh -i ~/Desktop/micontax/guevara-key-pair.pem ec2-user@184.72.148.3 \
  "sudo cp ~/bot /opt/go-tec/bot && sudo systemctl restart go-tec"
```

## Monitoring

```bash
# SSH in
ssh -i ~/Desktop/micontax/guevara-key-pair.pem ec2-user@184.72.148.3

# Live logs
sudo journalctl -u go-tec -f

# Bot status
sudo systemctl status go-tec

# API endpoints (from local machine)
curl http://184.72.148.3:8080/health
curl http://184.72.148.3:8080/status
curl http://184.72.148.3:8080/trades
curl http://184.72.148.3:8080/stats

# Download trade database for local analysis
scp -i ~/Desktop/micontax/guevara-key-pair.pem \
  ec2-user@184.72.148.3:/opt/go-tec/data/trades.db .
sqlite3 trades.db "SELECT * FROM trades ORDER BY opened_at DESC LIMIT 20"
```

## Troubleshooting

```bash
# Bot won't start
sudo journalctl -u go-tec --no-pager -n 50

# Check .env file
cat /opt/go-tec/.env

# Restart
sudo systemctl restart go-tec

# Check disk space
df -h

# Check memory
free -m

# Check if API port is listening
ss -tlnp | grep 8080

# Manual S3 backup
aws s3 cp /opt/go-tec/data/trades.db s3://go-trading-db-backups/backups/trades-manual.db

# Test SNS alert
aws sns publish --topic-arn arn:aws:sns:us-east-1:021363511692:go-trading-alerts \
  --subject "Test" --message "Test alert from EC2"
```

## Cost

| Resource | Monthly |
| --- | --- |
| t4g.nano on-demand | ~$3.07 |
| 8 GB gp3 EBS | ~$0.64 |
| S3 backups | ~$0.01 |
| SNS email alerts | ~$0.00 |
| **Total** | **~$3.72** |

## Notes

- **IP changes**: The public IP changes if you stop/start the instance. Consider an Elastic IP ($0/mo when attached to running instance) if this is annoying.
- **EBS survives termination**: `DeleteOnTermination=No` means your volume persists even if the instance is terminated. Attach it to a new instance to recover.
- **Security group = firewall**: No TLS/ACM needed. Only your IP can reach ports 22 and 8080.
- **No Docker**: Single Go binary, ~12MB, managed by systemd. Docker would waste ~100MB RAM on a 512MB instance.
