# EC2 Deployment Guide

Paper trading bot on AWS EC2, running 24/7 for data collection.

## Instance

| Setting       | Value                     |
| ------------- | ------------------------- |
| AMI           | Amazon Linux 2023 (ARM64) |
| Instance type | t4g.nano ($3.07/month)    |
| Storage       | 8 GB gp3 EBS (default)    |
| Key pair      | Your existing SSH key     |

## Security Group

**Inbound:** SSH (port 22) from your IP only. No other inbound rules -- the bot only makes outbound connections.

**Outbound:** All traffic (default).

## IAM Role (optional, for S3 backups)

Create an IAM role with this policy and attach it to the instance:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:PutObject"],
      "Resource": "arn:aws:s3:::YOUR-BUCKET/backups/*"
    }
  ]
}
```

## Deploy

### 1. Build the binary locally (cross-compile for ARM64)

```bash
GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o bot ./cmd/bot
```

### 2. Copy files to EC2

```bash
scp bot deploy/go-tec.service deploy/setup.sh ec2-user@<EC2-IP>:~/
```

### 3. SSH in and run the setup script

```bash
ssh ec2-user@<EC2-IP>
bash setup.sh
```

To enable daily S3 backups:

```bash
GO_TEC_S3_BUCKET=your-bucket-name bash setup.sh
```

## Operations

### Check status

```bash
sudo systemctl status go-tec
```

### Live logs

```bash
sudo journalctl -u go-tec -f
```

### Last hour of logs

```bash
sudo journalctl -u go-tec --since "1 hour ago"
```

### Restart after update

```bash
scp bot ec2-user@<EC2-IP>:~/
ssh ec2-user@<EC2-IP> "cp ~/bot /opt/go-tec/bot && sudo systemctl restart go-tec"
```

### Download trade database for analysis

```bash
scp ec2-user@<EC2-IP>:/opt/go-tec/data/trades.db ./trades.db
sqlite3 trades.db "SELECT * FROM trades ORDER BY opened_at DESC LIMIT 20;"
```

### Quick stats from the database

```bash
sqlite3 trades.db <<'SQL'
-- Win rate and P&L
SELECT
  COUNT(*) as total,
  SUM(CASE WHEN won=1 THEN 1 ELSE 0 END) as wins,
  ROUND(100.0 * SUM(CASE WHEN won=1 THEN 1 ELSE 0 END) / COUNT(*), 1) as win_rate,
  ROUND(SUM(pnl), 2) as total_pnl
FROM trades WHERE settled_at IS NOT NULL;

-- P&L by hour of day
SELECT
  strftime('%H', opened_at) as hour,
  COUNT(*) as trades,
  ROUND(SUM(pnl), 2) as pnl,
  ROUND(100.0 * SUM(CASE WHEN won=1 THEN 1 ELSE 0 END) / COUNT(*), 1) as win_rate
FROM trades WHERE settled_at IS NOT NULL
GROUP BY hour ORDER BY hour;

-- Calibration: predicted vs actual
SELECT
  CASE
    WHEN model_prob < 0.6 THEN '0.50-0.60'
    WHEN model_prob < 0.7 THEN '0.60-0.70'
    WHEN model_prob < 0.8 THEN '0.70-0.80'
    WHEN model_prob < 0.9 THEN '0.80-0.90'
    ELSE '0.90-1.00'
  END as bucket,
  ROUND(AVG(model_prob), 3) as avg_pred,
  ROUND(100.0 * SUM(CASE WHEN won=1 THEN 1 ELSE 0 END) / COUNT(*), 1) as actual_wr,
  COUNT(*) as n
FROM trades WHERE settled_at IS NOT NULL
GROUP BY bucket ORDER BY bucket;
SQL
```

## Cost Estimate

| Resource               | Monthly Cost     |
| ---------------------- | ---------------- |
| t4g.nano (on-demand)   | ~$3.07           |
| 8 GB gp3 EBS           | ~$0.64           |
| S3 backups (~1 MB/day) | ~$0.01           |
| **Total**              | **~$3.72/month** |

With a 1-year reserved instance, t4g.nano drops to ~$1.75/month.
