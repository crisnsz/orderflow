package rabbitmq

import "time"

const maxBackoffShift = 30

type RetryPolicy struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
}

var DefaultRetryPolicy = RetryPolicy{BaseDelay: time.Second, MaxDelay: 8 * time.Second, MaxAttempts: 6}

func (p RetryPolicy) retriesEnabled() bool { return p.MaxAttempts > 1 }

func (p RetryPolicy) deliveryLimit() int64 { return int64(p.MaxAttempts - 1) }

func (p RetryPolicy) DelayAfter(failedAttempts int) time.Duration {
	shift := min(max(failedAttempts-1, 0), maxBackoffShift)
	return min(p.BaseDelay<<shift, p.MaxDelay)
}

func (p RetryPolicy) exhaustedAfter(failedAttempts int) bool { return failedAttempts >= p.MaxAttempts }
