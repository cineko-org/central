package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const integrationPostgresImage = "postgres:18.1-alpine3.23"

var testDatabaseURL string

func TestMain(testMain *testing.M) {
	testDatabaseURL = os.Getenv("CINEKO_CENTRAL_TEST_DATABASE_URL")
	if testDatabaseURL != "" || os.Getenv("CINEKO_CENTRAL_INTEGRATION") != "1" {
		os.Exit(testMain.Run())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	container, err := postgrescontainer.Run(
		ctx,
		integrationPostgresImage,
		postgrescontainer.WithDatabase("cineko"),
		postgrescontainer.WithUsername("cineko"),
		postgrescontainer.WithPassword("cineko_test"),
		testcontainers.WithImagePlatform("linux/amd64"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		cancel()
		_, _ = fmt.Fprintf(os.Stderr, "start PostgreSQL testcontainer: %v\n", err)
		os.Exit(1)
	}
	testDatabaseURL, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = testcontainers.TerminateContainer(container)
		cancel()
		_, _ = fmt.Fprintf(os.Stderr, "read PostgreSQL testcontainer URL: %v\n", err)
		os.Exit(1)
	}
	cancel()

	code := testMain.Run()
	if err := testcontainers.TerminateContainer(container); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "terminate PostgreSQL testcontainer: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
