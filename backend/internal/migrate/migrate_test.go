package migrate

import "testing"

func TestMigrationsLoadInContiguousOrder(t *testing.T) {
	migrations, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migrations) == 0 {
		t.Fatal("no migrations were embedded — check the go:embed directive")
	}
	for i, m := range migrations {
		if m.Version != i+1 {
			t.Fatalf("migration %d has version %d", i, m.Version)
		}
		if m.SQL == "" {
			t.Fatalf("%s is empty", m.Name)
		}
	}
}
