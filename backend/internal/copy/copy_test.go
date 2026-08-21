package copy

import "testing"

func TestResolveInterpolates(t *testing.T) {
	got, err := Resolve(KeyEmailLoginCodeSubject, map[string]string{"code": "482913"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want := "Your Pod code: 482913"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveRejectsMissingPlaceholder(t *testing.T) {
	if _, err := Resolve(KeyEmailLoginCodeSubject, nil); err == nil {
		t.Fatal("expected an error for an unfilled placeholder — half-interpolated copy reaches a person")
	}
}

func TestResolveUnknownKey(t *testing.T) {
	if _, err := Resolve(Key("nope.not.a.key"), nil); err == nil {
		t.Fatal("expected an error for an unknown key")
	}
}

func TestResolvePluralPicksForm(t *testing.T) {
	one, err := ResolvePlural(KeyStatePodWeeksTogether, 1, map[string]string{"count": "1"})
	if err != nil {
		t.Fatalf("ResolvePlural(1): %v", err)
	}
	many, err := ResolvePlural(KeyStatePodWeeksTogether, 6, map[string]string{"count": "6"})
	if err != nil {
		t.Fatalf("ResolvePlural(6): %v", err)
	}
	if one == many {
		t.Fatalf("singular and plural forms should differ, both were %q", one)
	}
	if want := "1 week together"; one != want {
		t.Fatalf("got %q, want %q", one, want)
	}
	if want := "6 weeks together"; many != want {
		t.Fatalf("got %q, want %q", many, want)
	}
}

// Every generated key must be present in the catalogue map — otherwise a handler could
// name a key that compiles but resolves to nothing.
func TestEveryGeneratedKeyResolves(t *testing.T) {
	for _, key := range AllKeys {
		if !Has(key) {
			t.Errorf("key %q is declared but missing from the catalogue", key)
		}
	}
}
