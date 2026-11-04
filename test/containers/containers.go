//go:build integration

package containers

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	postgresImage = "postgres:17.6-alpine"
	rabbitImage   = "rabbitmq:4.3.6-management-alpine"
	rabbitUser    = "test"
	rabbitPass    = "test"
	amqpPort      = "5672/tcp"
	rabbitStartup = 60 * time.Second
)

var (
	postgresOnce     sync.Once
	postgresBaseURL  string
	postgresStartErr error
	databaseCount    atomic.Int64

	rabbitmqOnce     sync.Once
	rabbitmqURL      string
	rabbitmqStartErr error
)

func Postgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	postgresOnce.Do(func() {
		container, err := postgres.Run(ctx, postgresImage,
			postgres.WithDatabase("postgres"), postgres.WithUsername("test"), postgres.WithPassword("test"),
			postgres.BasicWaitStrategies())
		if err != nil {
			postgresStartErr = err
			return
		}
		postgresBaseURL, postgresStartErr = container.ConnectionString(ctx, "sslmode=disable")
	})
	if postgresStartErr != nil {
		t.Fatalf("start postgres container (is Docker running?): %v", postgresStartErr)
	}
	admin, err := pgxpool.New(ctx, postgresBaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	databaseName := fmt.Sprintf("t%d", databaseCount.Add(1))
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+databaseName); err != nil {
		t.Fatal(err)
	}
	databaseURL, _ := url.Parse(postgresBaseURL)
	databaseURL.Path = "/" + databaseName
	pool, err := pgxpool.New(ctx, databaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func RabbitMQ(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	rabbitmqOnce.Do(func() {
		container, err := runRabbitMQ(ctx)
		if err != nil {
			rabbitmqStartErr = err
			return
		}
		rabbitmqURL, rabbitmqStartErr = amqpURL(ctx, container)
	})
	if rabbitmqStartErr != nil {
		t.Fatalf("start rabbitmq container (is Docker running?): %v", rabbitmqStartErr)
	}
	return rabbitmqURL
}

func runRabbitMQ(ctx context.Context, customizers ...testcontainers.ContainerCustomizer) (*testcontainers.DockerContainer, error) {
	options := []testcontainers.ContainerCustomizer{
		testcontainers.WithEnv(map[string]string{"RABBITMQ_DEFAULT_USER": rabbitUser, "RABBITMQ_DEFAULT_PASS": rabbitPass}),
		testcontainers.WithExposedPorts(amqpPort),
		testcontainers.WithWaitStrategy(wait.ForLog(".*Server startup complete.*").AsRegexp().WithStartupTimeout(rabbitStartup)),
	}
	return testcontainers.Run(ctx, rabbitImage, append(options, customizers...)...)
}

func amqpURL(ctx context.Context, container *testcontainers.DockerContainer) (string, error) {
	endpoint, err := container.PortEndpoint(ctx, amqpPort, "")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("amqp://%s:%s@%s/", rabbitUser, rabbitPass, endpoint), nil
}
