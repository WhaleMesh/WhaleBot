package store

import "testing"

func TestFtsMatchQuery(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"AI", `"AI"`},
		{"看一下 AI 行业动态", `("看一下" OR "AI" OR "行业动态")`},
		{"foo-bar/baz", `("foo" OR "bar" OR "baz")`},
	}
	for _, tc := range tests {
		if got := ftsMatchQuery(tc.in); got != tc.want {
			t.Fatalf("ftsMatchQuery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSlugify(t *testing.T) {
	if got := Slugify("AI Hot"); got != "ai-hot" {
		t.Fatalf("Slugify = %q", got)
	}
}
