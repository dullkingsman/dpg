//go:build integration

// Package testpg provides a PostgreSQL container for integration tests.
// Import it in integration test files tagged with //go:build integration.
package testpg

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Start launches a Postgres container, registers t.Cleanup to stop it, and
// returns the connection string. The container is ready to accept connections
// when Start returns.
func Start(t *testing.T) string {
	t.Helper()
	return startContainer(t)
}

// startFromExisting connects to an existing Postgres server via adminURL,
// creates a fresh database named after the test, and returns a connection
// string for that database. The database is dropped in t.Cleanup.
func startFromExisting(t *testing.T, adminURL string) string {
	t.Helper()
	ctx := context.Background()

	// Derive a safe database name from the test name.
	safeName := regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(
		strings.ToLower(t.Name()), "_",
	)
	if len(safeName) > 50 {
		safeName = safeName[:50]
	}
	dbName := "dpgtest_" + safeName

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("testpg: connect to existing server: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		admin.Close(ctx)
		t.Fatalf("testpg: create test database %q: %v", dbName, err)
	}
	admin.Close(ctx)

	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), adminURL)
		if err != nil {
			t.Errorf("testpg: cleanup connect: %v", err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)"); err != nil {
			t.Errorf("testpg: drop test database %q: %v", dbName, err)
		}
	})

	cfg, err := pgx.ParseConfig(adminURL)
	if err != nil {
		t.Fatalf("testpg: parse admin URL: %v", err)
	}
	cfg.Database = dbName
	return cfg.ConnString()
}

// startContainer starts a fresh postgres:17-alpine testcontainer.
func startContainer(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:17",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_USER":     "dpg",
			"POSTGRES_PASSWORD": "dpg",
			"POSTGRES_DB":       "dpgtest",
		},
		WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("testpg: start container: %v", err)
	}

	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Errorf("testpg: terminate container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("testpg: get host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432")
	if err != nil {
		t.Fatalf("testpg: get port: %v", err)
	}

	return fmt.Sprintf("postgres://dpg:dpg@%s:%s/dpgtest?sslmode=disable", host, port.Port())
}
