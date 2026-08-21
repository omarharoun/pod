package migrate_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/omarharoun/pod/backend/internal/migrate"
)

// Every migration must apply cleanly to an empty database, every time.
//
// The store tests do run migrate.Up, but against a database that is already migrated,
// where Up is a no-op. A broken new migration would therefore only fail for whoever
// happened to run it first on a fresh schema — which is to say, in production. This test
// creates a throwaway database so the whole sequence runs from nothing on every `make
// verify`.
func TestEveryMigrationAppliesToAnEmptyDatabase(t *testing.T) {
	admin := os.Getenv("TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("set TEST_DATABASE_URL to run the migration integration test")
	}
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()

	name := fmt.Sprintf("pod_migrate_%d", os.Getpid())
	// A developer with a restricted database user may legitimately not be able to create
	// one, and skipping is right for them. In CI it is not: a silent skip here would
	// quietly remove the only check that migrations apply from nothing, which is the
	// exact shape of bug this file exists to catch. POD_REQUIRE_MIGRATION_TEST makes the
	// skip a failure, and the CI workflow sets it.
	cannot := func(err error) {
		t.Helper()
		if os.Getenv("POD_REQUIRE_MIGRATION_TEST") != "" {
			t.Fatalf("cannot create databases, and POD_REQUIRE_MIGRATION_TEST is set: %v", err)
		}
		t.Skipf("cannot create databases with this connection: %v", err)
	}
	if _, err := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize()); err != nil {
		cannot(err)
	}
	if _, err := conn.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize()); err != nil {
		cannot(err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), `DROP DATABASE IF EXISTS `+pgx.Identifier{name}.Sanitize())
	})

	fresh, err := pgx.Connect(ctx, replaceDatabase(admin, name))
	if err != nil {
		t.Fatalf("connect to the fresh database: %v", err)
	}
	defer func() { _ = fresh.Close(ctx) }()

	applied, err := migrate.Up(ctx, fresh)
	if err != nil {
		t.Fatalf("migrations do not apply to an empty database: %v", err)
	}
	all, err := migrate.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(applied) != len(all) {
		t.Errorf("applied %d of %d migrations", len(applied), len(all))
	}

	// And running them again changes nothing, because the job that applies them may run
	// twice — on two instances starting at once, for one.
	again, err := migrate.Up(ctx, fresh)
	if err != nil {
		t.Fatalf("re-running the migrations failed: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("re-running applied %d migrations: %v", len(again), again)
	}
}

// replaceDatabase swaps the database name in a postgres URL, keeping everything else.
func replaceDatabase(url, name string) string {
	base, query, hasQuery := strings.Cut(url, "?")
	slash := strings.LastIndex(base, "/")
	if slash < 0 {
		return url
	}
	out := base[:slash+1] + name
	if hasQuery {
		out += "?" + query
	}
	return out
}
