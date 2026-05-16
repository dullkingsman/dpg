//go:build integration

// Package testpg provides a PostgreSQL container for integration tests.
// Import it in integration test files tagged with //go:build integration.
package testpg

import (
	"context"
	"fmt"
	"testing"

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
