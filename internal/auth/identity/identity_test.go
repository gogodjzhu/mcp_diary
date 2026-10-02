package identity

import "testing"

func TestIdentitySlug(t *testing.T) {
	cases := []struct {
		identity Identity
		want     string
	}{
		{Identity{Email: "Alice@Example.com"}, "alice@example.com"},
		{Identity{Email: "a/b/c@example.com"}, "a_b_c@example.com"},
		{Identity{Subject: "1178"}, "1178"},
		{Identity{Email: "../../etc/passwd"}, "etc_passwd"},
		{Identity{}, "user"},
	}

	for _, tc := range cases {
		if got := tc.identity.Slug(); got != tc.want {
			t.Fatalf("Slug(%+v) = %q, want %q", tc.identity, got, tc.want)
		}
	}
}

func TestIdentityUsernamePreference(t *testing.T) {
	if got := (Identity{Email: "a@b.com", Name: "Alice", Subject: "1"}).Username(); got != "a@b.com" {
		t.Fatalf("username = %q", got)
	}
	if got := (Identity{Name: "Alice", Subject: "1"}).Username(); got != "Alice" {
		t.Fatalf("username = %q", got)
	}
	if got := (Identity{Subject: "1"}).Username(); got != "1" {
		t.Fatalf("username = %q", got)
	}
}
