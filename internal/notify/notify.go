package notify

import (
	"fmt"
	"log/slog"
	"os/exec"
)

// Notifier sends alerts via AWS SNS using the AWS CLI.
// Requires AWS CLI installed and IAM credentials available (EC2 instance role).
// If topicARN is empty, all calls are no-ops.
type Notifier struct {
	topicARN string
	logger   *slog.Logger
}

func New(topicARN string, logger *slog.Logger) *Notifier {
	return &Notifier{topicARN: topicARN, logger: logger}
}

// Enabled returns true if an SNS topic is configured.
func (n *Notifier) Enabled() bool {
	return n.topicARN != ""
}

// Send publishes a message to the configured SNS topic asynchronously.
// Failures are logged but never block the caller.
func (n *Notifier) Send(subject, message string) {
	if n.topicARN == "" {
		return
	}
	go func() {
		cmd := exec.Command("aws", "sns", "publish",
			"--topic-arn", n.topicARN,
			"--subject", subject,
			"--message", message,
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			n.logger.Warn("sns publish failed", "err", err, "output", string(output))
			return
		}
		n.logger.Debug("sns alert sent", "subject", subject)
	}()
}

// Alert sends a formatted alert with the [go-tec] prefix.
func (n *Notifier) Alert(event, details string) {
	n.Send(fmt.Sprintf("[go-tec] %s", event), details)
}
