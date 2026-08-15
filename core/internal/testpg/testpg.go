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
	connStr, _ := startContainer(t, nil)
	return connStr
}

// StartLogical is Start with wal_level=logical set — the prerequisite for a
// publisher database used with CREATE PUBLICATION/SUBSCRIPTION. Postgres's
// default wal_level (replica) rejects a subscriber's initial sync outright,
// so a real CREATE SUBSCRIPTION round trip needs its publisher started this
// way specifically (the subscriber side has no such requirement).
func StartLogical(t *testing.T) string {
	t.Helper()
	connStr, _ := startContainer(t, []string{"postgres", "-c", "wal_level=logical"})
	return connStr
}

// StartWithContainer is Start, additionally returning the underlying
// testcontainers.Container — needed only by tests that must reach the
// container's filesystem directly (e.g. mkdir-ing a real path for
// CREATE TABLESPACE, which PostgreSQL requires to already exist on disk;
// no SQL statement can create it).
func StartWithContainer(t *testing.T) (string, testcontainers.Container) {
	t.Helper()
	return startContainer(t, nil)
}

// startContainer starts a fresh postgres:17 testcontainer. cmd overrides the
// container's default command when non-nil (e.g. to set wal_level).
func startContainer(t *testing.T, cmd []string) (string, testcontainers.Container) {
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
	if cmd != nil {
		req.Cmd = cmd
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

	return fmt.Sprintf("postgres://dpg:dpg@%s:%s/dpgtest?sslmode=disable", host, port.Port()), container
}
