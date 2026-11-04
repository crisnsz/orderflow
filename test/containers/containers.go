//go:build integration

package containers

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

const (
	postgresImage = "postgres:17.6-alpine"
	rabbitImage   = "rabbitmq:4.3.6-management-alpine"
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
		container, err := rabbitmq.Run(ctx, rabbitImage)
		if err != nil {
			rabbitmqStartErr = err
			return
		}
		rabbitmqURL, rabbitmqStartErr = container.AmqpURL(ctx)
	})
	if rabbitmqStartErr != nil {
		t.Fatalf("start rabbitmq container (is Docker running?): %v", rabbitmqStartErr)
	}
	return rabbitmqURL
}
