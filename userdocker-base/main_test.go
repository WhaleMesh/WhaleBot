package main

import "testing"

func TestResolveWorkspacePath(t *testing.T) {
	cases := []struct {
		in, want string
		wantErr  bool
	}{
		// The regression that broke E2E: absolute /workspace paths must not double.
		{"/workspace/main.go", "/workspace/main.go", false},
		{"/workspace", "/workspace", false},
		{"/workspace/sub/dir/file", "/workspace/sub/dir/file", false},
		// Relative and bare-absolute inputs are rooted at the workspace.
		{"main.go", "/workspace/main.go", false},
		{"/main.go", "/workspace/main.go", false},
		{"", "/workspace", false},
		{".", "/workspace", false},
		{"/", "/workspace", false},
		// Similar prefix is not the root.
		{"/workspacefoo/x", "/workspace/workspacefoo/x", false},
		// Escapes stay rejected; cleaned absolute paths get re-rooted safely.
		{"../etc/passwd", "", true},
		{"/workspace/../etc/passwd", "/workspace/etc/passwd", false},
	}
	for _, c := range cases {
		got, err := resolveWorkspacePath("/workspace", c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("resolveWorkspacePath(%q): expected error, got %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveWorkspacePath(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("resolveWorkspacePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
