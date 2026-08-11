package creator

import "testing"

func TestParseImageRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in                         string
		wantReg, wantRepo, wantTag string
	}{
		{"python:3.12", "docker.io", "library/python", "3.12"},
		{"python", "docker.io", "library/python", "latest"},
		{"grafana/grafana:10.0", "docker.io", "grafana/grafana", "10.0"},
		{"ghcr.io/acme/app:v1", "ghcr.io", "acme/app", "v1"},
		{"localhost:5000/img:dev", "localhost:5000", "img", "dev"},
	}
	for _, c := range cases {
		reg, repo, tag := parseImageRef(c.in)
		if reg != c.wantReg || repo != c.wantRepo || tag != c.wantTag {
			t.Errorf("parseImageRef(%q) = (%q,%q,%q), want (%q,%q,%q)",
				c.in, reg, repo, tag, c.wantReg, c.wantRepo, c.wantTag)
		}
	}
}

func TestDemuxDockerStream(t *testing.T) {
	t.Parallel()
	// One stdout frame (stream type 1) carrying "hi".
	framed := []byte{1, 0, 0, 0, 0, 0, 0, 2, 'h', 'i'}
	if got := demuxDockerStream(framed); got != "hi" {
		t.Fatalf("framed: got %q want %q", got, "hi")
	}
	// Non-framed payload (TTY) is returned unchanged.
	plain := []byte("plain log line\n")
	if got := demuxDockerStream(plain); got != string(plain) {
		t.Fatalf("plain: got %q want %q", got, string(plain))
	}
}

func TestMapDockerImageList(t *testing.T) {
	t.Parallel()
	raw := []dockerImageListEntry{
		{ID: "sha256:aaaaaaaaaaaa1111", RepoTags: []string{"alpine:3.20"}, Size: 100, Created: 100},
		{ID: "sha256:bbbbbbbbbbbb2222", RepoTags: []string{"<none>:<none>"}, Size: 50, Created: 200},
		{ID: "sha256:cccccccccccccccc", RepoTags: nil, Size: 10, Created: 150},
	}
	got := mapDockerImageList(raw)
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	// Newest first.
	if got[0].ID != "bbbbbbbbbbbb" || len(got[0].RepoTags) != 0 {
		t.Fatalf("newest dangling: %+v", got[0])
	}
	if got[1].ID != "cccccccccccc" || got[1].RepoTags == nil || len(got[1].RepoTags) != 0 {
		t.Fatalf("mid: %+v", got[1])
	}
	if got[2].ID != "aaaaaaaaaaaa" || got[2].RepoTags[0] != "alpine:3.20" {
		t.Fatalf("oldest tagged: %+v", got[2])
	}
}
