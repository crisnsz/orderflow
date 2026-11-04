//go:build integration

package containers

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	dockercontainer "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

const (
	amqpContainerPort = "5672/tcp"
	brokerStopTimeout = 10 * time.Second
)

type RestartableRabbitMQ struct {
	t         *testing.T
	container *rabbitmq.RabbitMQContainer
	URL       string
}

func StartRestartableRabbitMQ(t *testing.T) *RestartableRabbitMQ {
	t.Helper()
	ctx := context.Background()
	hostPort := freeTCPPort(t)
	broker, err := rabbitmq.Run(ctx, rabbitImage,
		testcontainers.WithHostConfigModifier(func(hostConfig *dockercontainer.HostConfig) {
			hostConfig.PortBindings = network.PortMap{
				network.MustParsePort(amqpContainerPort): {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: hostPort}},
			}
		}))
	if err != nil {
		t.Fatalf("start rabbitmq container (is Docker running?): %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(broker) })
	return &RestartableRabbitMQ{t: t, container: broker, URL: fmt.Sprintf("amqp://guest:guest@127.0.0.1:%s/", hostPort)}
}

func (b *RestartableRabbitMQ) Stop() {
	b.t.Helper()
	timeout := brokerStopTimeout
	if err := b.container.Stop(context.Background(), &timeout); err != nil {
		b.t.Fatal(err)
	}
}

func (b *RestartableRabbitMQ) Start() {
	b.t.Helper()
	if err := b.container.Start(context.Background()); err != nil {
		b.t.Fatal(err)
	}
}

func freeTCPPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port
}

func FreeTCPAddress(t *testing.T) string {
	t.Helper()
	return "127.0.0.1:" + freeTCPPort(t)
}
