package player

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

// resetCookieDir makes the session directory for private cookie copies start
// under this test's TMPDIR and removes it afterwards.
func resetCookieDir(t *testing.T) {
	t.Helper()
	ytdlcookies.Shutdown()
	t.Cleanup(ytdlcookies.Shutdown)
}

// cookieCopies lists the private cookie copies in the session directory
// created under tmpDir.
func cookieCopies(t *testing.T, tmpDir string) []string {
	t.Helper()
	copies, err := filepath.Glob(filepath.Join(tmpDir, "cliamp-cookies-*", "*"))
	if err != nil {
		t.Fatal(err)
	}
	return copies
}

func TestAppendYTDLCookieArgsSelectsSourceByURLHost(t *testing.T) {
	cookieFile := filepath.Join(t.TempDir(), "netease.txt")
	if err := os.WriteFile(cookieFile, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ytdlcookies.SetForHost("mixcloud.com", ytdlcookies.Source{Browser: "firefox"})
	ytdlcookies.SetForHost("music.163.com", ytdlcookies.Source{File: cookieFile})
	t.Cleanup(func() {
		ytdlcookies.SetForHost("mixcloud.com", ytdlcookies.Source{})
		ytdlcookies.SetForHost("music.163.com", ytdlcookies.Source{})
	})

	tests := []struct {
		url  string
		want []string
	}{
		{
			url:  "https://www.mixcloud.com/creator/show/",
			want: []string{"yt-dlp", "--cookies-from-browser", "firefox"},
		},
		{
			url:  "https://music.163.com/#/song?id=1",
			want: []string{"yt-dlp", "--cookies"}, // followed by the private copy's path
		},
		{
			url:  "https://example.com/track",
			want: []string{"yt-dlp"},
		},
	}

	for _, test := range tests {
		got, cleanup, err := appendYTDLCookieArgs([]string{"yt-dlp"}, test.url)
		cleanup()
		if err != nil {
			t.Fatalf("appendYTDLCookieArgs(%q) error = %v", test.url, err)
		}
		if slices.Contains(test.want, "--cookies") {
			got = got[:min(len(got), len(test.want))] // the copy path is ytdlcookies' contract
		}
		if !slices.Equal(got, test.want) {
			t.Errorf("appendYTDLCookieArgs(%q) = %v, want %v", test.url, got, test.want)
		}
	}
}
