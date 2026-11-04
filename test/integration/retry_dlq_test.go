//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/crisnsz/orderflow/internal/platform/events"
	"github.com/crisnsz/orderflow/internal/platform/rabbitmq"
	"github.com/crisnsz/orderflow/test/containers"
	"github.com/rabbitmq/rabbitmq-amqp-go-client/pkg/rabbitmqamqp"
)

const delayTolerance = 150 * time.Millisecond

type attemptRecorder struct {
	mu       sync.Mutex
	attempts []time.Time
}

func (r *attemptRecorder) record() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts = append(r.attempts, time.Now())
	return len(r.attempts)
}

func (r *attemptRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.attempts)
}

func (r *attemptRecorder) gaps() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	var gaps []time.Duration
	for i := 1; i < len(r.attempts); i++ {
		gaps = append(gaps, r.attempts[i].Sub(r.attempts[i-1]))
	}
	return gaps
}

func startConsumer(t *testing.T, spec rabbitmq.QueueSpec, handle rabbitmq.Handler) *rabbitmqamqp.AmqpConnection {
	t.Helper()
	connection := connect(t, containers.RabbitMQ(t))
	ctx, cancel := context.WithCancel(context.Background())
	if err := rabbitmq.Declare(ctx, connection, spec); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = rabbitmq.Consume(ctx, connection, spec, handle) }()
	t.Cleanup(func() { cancel(); <-done })
	publishOne(t, connection, spec.Keys[0])
	return connection
}

func publishOne(t *testing.T, connection *rabbitmqamqp.AmqpConnection, routingKey string) {
	t.Helper()
	publisher, err := rabbitmq.NewPublisher(context.Background(), connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(publisher.Close)
	message, _ := events.New(routingKey, events.Payload{OrderID: "order-1", UserID: "u"})
	if err := publisher.Publish(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}

func assertGapsFollowBackoff(t *testing.T, got []time.Duration, policy rabbitmq.RetryPolicy) {
	t.Helper()
	for i, gap := range got {
		want := policy.DelayAfter(i + 1)
		if gap < want-delayTolerance || gap > want+delayTolerance+time.Second {
			t.Errorf("gap before attempt %d = %v, want about %v", i+2, gap, want)
		}
	}
}

func TestFailingMessageIsRetriedWithExponentialBackoffThenDeadLettered(t *testing.T) {
	policy := rabbitmq.RetryPolicy{BaseDelay: 300 * time.Millisecond, MaxDelay: 1200 * time.Millisecond, MaxAttempts: 5}
	spec := rabbitmq.QueueSpec{Name: "test.dlq.fail.q", Keys: []string{"test.dlq.fail"}, Retry: policy}
	var recorder attemptRecorder
	connection := startConsumer(t, spec, func(context.Context, events.Message) error {
		recorder.record()
		return errors.New("downstream unavailable")
	})

	deadLetter := receiveDeadLetter(t, connection, spec.Name)
	if got := recorder.count(); got != policy.MaxAttempts {
		t.Errorf("handler calls = %d, want %d", got, policy.MaxAttempts)
	}
	assertGapsFollowBackoff(t, recorder.gaps(), policy)
	if *deadLetter.Properties.Subject != "test.dlq.fail" || deadLetter.Properties.CorrelationID != "order-1" {
		t.Errorf("dead letter lost its metadata: %+v", deadLetter.Properties)
	}
	if deadLetter.Annotations["x-last-error"] != "downstream unavailable" {
		t.Errorf("x-last-error = %v, annotations = %v", deadLetter.Annotations["x-last-error"], deadLetter.Annotations)
	}
	if depth := queueDepth(t, connection, spec.Name); depth != 0 {
		t.Errorf("main queue depth = %d, want 0", depth)
	}
}

func TestPermanentFailureGoesStraightToDLQ(t *testing.T) {
	spec := rabbitmq.QueueSpec{Name: "test.dlq.perm.q", Keys: []string{"test.dlq.perm"}, Retry: rabbitmq.DefaultRetryPolicy}
	var recorder attemptRecorder
	connection := startConsumer(t, spec, func(context.Context, events.Message) error {
		recorder.record()
		return errors.Join(rabbitmq.ErrPermanent, errors.New("malformed body"))
	})

	started := time.Now()
	deadLetter := receiveDeadLetter(t, connection, spec.Name)
	if elapsed := time.Since(started); elapsed > rabbitmq.DefaultRetryPolicy.BaseDelay {
		t.Errorf("permanent failure took %v to reach the DLQ, want no retry delay", elapsed)
	}
	if got := recorder.count(); got != 1 {
		t.Errorf("handler calls = %d, want 1", got)
	}
	if deadLetter.Annotations["x-last-error"] == nil {
		t.Errorf("dead letter has no x-last-error: %v", deadLetter.Annotations)
	}
}

func TestQueueWithoutRetryPolicyDeadLettersOnFirstFailure(t *testing.T) {
	spec := rabbitmq.QueueSpec{Name: "test.dlq.noretry.q", Keys: []string{"test.dlq.noretry"}}
	var recorder attemptRecorder
	connection := startConsumer(t, spec, func(context.Context, events.Message) error {
		recorder.record()
		return errors.New("boom")
	})

	receiveDeadLetter(t, connection, spec.Name)
	if got := recorder.count(); got != 1 {
		t.Errorf("handler calls = %d, want 1", got)
	}
}

func TestPanickingHandlerIsRetriedLikeAnyFailure(t *testing.T) {
	policy := rabbitmq.RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 200 * time.Millisecond, MaxAttempts: 3}
	spec := rabbitmq.QueueSpec{Name: "test.dlq.panic.q", Keys: []string{"test.dlq.panic"}, Retry: policy}
	var recorder attemptRecorder
	connection := startConsumer(t, spec, func(context.Context, events.Message) error {
		recorder.record()
		panic("handler bug")
	})

	deadLetter := receiveDeadLetter(t, connection, spec.Name)
	if got := recorder.count(); got != policy.MaxAttempts {
		t.Errorf("handler calls = %d, want %d", got, policy.MaxAttempts)
	}
	if deadLetter.Annotations["x-last-error"] != "handler panic: handler bug" {
		t.Errorf("x-last-error = %v", deadLetter.Annotations["x-last-error"])
	}
}

func TestTransientFailureRecoversWithoutDLQ(t *testing.T) {
	policy := rabbitmq.RetryPolicy{BaseDelay: 300 * time.Millisecond, MaxDelay: 1200 * time.Millisecond, MaxAttempts: 5}
	spec := rabbitmq.QueueSpec{Name: "test.dlq.ok.q", Keys: []string{"test.dlq.ok"}, Retry: policy}
	var recorder attemptRecorder
	connection := startConsumer(t, spec, func(context.Context, events.Message) error {
		if recorder.record() < 3 {
			return errors.New("temporary")
		}
		return nil
	})

	waitFor(t, "third attempt", func() bool { return recorder.count() == 3 })
	time.Sleep(time.Second)
	if got := recorder.count(); got != 3 {
		t.Errorf("handler calls = %d, want 3", got)
	}
	assertGapsFollowBackoff(t, recorder.gaps(), policy)
	for _, queueName := range []string{spec.Name, rabbitmq.DLQName(spec.Name)} {
		if depth := queueDepth(t, connection, queueName); depth != 0 {
			t.Errorf("%s depth = %d, want 0", queueName, depth)
		}
	}
}

func TestDeliveryCountAdvancesOnFailedReturnAndBrokerDeadLettersAtLimit(t *testing.T) {
	policy := rabbitmq.RetryPolicy{BaseDelay: 50 * time.Millisecond, MaxDelay: 50 * time.Millisecond, MaxAttempts: 4}
	spec := rabbitmq.QueueSpec{Name: "test.dlq.limit.q", Keys: []string{"test.dlq.limit"}, Retry: policy}
	connection := connect(t, containers.RabbitMQ(t))
	if err := rabbitmq.Declare(context.Background(), connection, spec); err != nil {
		t.Fatal(err)
	}
	publishOne(t, connection, spec.Keys[0])

	consumer, err := connection.NewConsumer(context.Background(), spec.Name, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consumer.Close(context.Background()) }()
	var deliveryCounts []uint32
	for {
		receiveCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		delivery, err := consumer.Receive(receiveCtx)
		cancel()
		if err != nil {
			break
		}
		deliveryCounts = append(deliveryCounts, delivery.Message().Header.DeliveryCount)
		if err := delivery.DelayRetry(context.Background(), 50*time.Millisecond, true); err != nil {
			t.Fatal(err)
		}
	}

	wantCounts := []uint32{0, 1, 2, 3}
	if len(deliveryCounts) != len(wantCounts) {
		t.Fatalf("delivery counts = %v, want %v", deliveryCounts, wantCounts)
	}
	for i := range wantCounts {
		if deliveryCounts[i] != wantCounts[i] {
			t.Fatalf("delivery counts = %v, want %v", deliveryCounts, wantCounts)
		}
	}
	receiveDeadLetter(t, connection, spec.Name)
}

func TestSlowHandlerDoesNotStallHeartbeats(t *testing.T) {
	const idleTimeout = 2 * time.Second
	const handlerBlockTime = 3 * idleTimeout
	connection := connect(t, containers.RabbitMQ(t), rabbitmq.WithIdleTimeout(idleTimeout))
	spec := rabbitmq.QueueSpec{Name: "test.slow.handler.q", Keys: []string{"test.slow.handler"}, Retry: rabbitmq.DefaultRetryPolicy}
	if err := rabbitmq.Declare(context.Background(), connection, spec); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var handled sync.WaitGroup
	handled.Add(1)
	go func() {
		defer close(done)
		_ = rabbitmq.Consume(ctx, connection, spec, func(context.Context, events.Message) error {
			defer handled.Done()
			time.Sleep(handlerBlockTime)
			return nil
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	publishOne(t, connection, spec.Keys[0])

	handled.Wait()
	if _, open := connection.State().(*rabbitmqamqp.StateOpen); !open {
		t.Fatalf("connection state = %T after a handler blocked for %v with idle timeout %v", connection.State(), handlerBlockTime, idleTimeout)
	}
	waitFor(t, "slow message settled", func() bool { return queueDepth(t, connection, spec.Name) == 0 })
}
