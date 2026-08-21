package store_test

import (
	"context"
	"testing"
	"time"
)

// The comparison point for the handler benchmarks: what one database round trip costs on
// the same machine, over loopback — i.e. the best case, with no network between.
func BenchmarkGetUser(b *testing.B) {
	db := newPostgres(b)
	ctx := context.Background()
	user, err := db.UpsertUserByEmail(ctx, "bench@example.com", time.Now().UTC())
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := db.GetUser(ctx, user.ID); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPing(b *testing.B) {
	db := newPostgres(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := db.Ping(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
