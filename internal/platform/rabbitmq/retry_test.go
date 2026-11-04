package rabbitmq

import (
	"testing"
	"time"
)

func TestDelayAfterDoublesUntilTheCap(t *testing.T) {
	policy := RetryPolicy{BaseDelay: time.Second, MaxDelay: 8 * time.Second, MaxAttempts: 8}
	tests := []struct {
		failedAttempts int
		want           time.Duration
	}{
		{1, time.Second}, {2, 2 * time.Second}, {3, 4 * time.Second}, {4, 8 * time.Second}, {5, 8 * time.Second}, {100, 8 * time.Second},
	}
	for _, tt := range tests {
		if got := policy.DelayAfter(tt.failedAttempts); got != tt.want {
			t.Errorf("DelayAfter(%d) = %v, want %v", tt.failedAttempts, got, tt.want)
		}
	}
}

func TestRetriesNeedMoreThanOneAttempt(t *testing.T) {
	if (RetryPolicy{}).retriesEnabled() || (RetryPolicy{MaxAttempts: 1}).retriesEnabled() {
		t.Error("a policy with one attempt must not retry")
	}
	if got := DefaultRetryPolicy.deliveryLimit(); got != int64(DefaultRetryPolicy.MaxAttempts-1) {
		t.Errorf("deliveryLimit = %d", got)
	}
}
