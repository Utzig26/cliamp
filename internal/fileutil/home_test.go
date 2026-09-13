package fileutil

import (
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		in, want string
	}{
		{"~", home},
		{"~/cookies.txt", filepath.Join(home, "cookies.txt")},
		{"~/a/b", filepath.Join(home, "a", "b")},
		{"~user/x", "~user/x"},
		{"/abs/path", "/abs/path"},
		{"rel/path", "rel/path"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := ExpandHome(tc.in); got != tc.want {
			t.Errorf("ExpandHome(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
