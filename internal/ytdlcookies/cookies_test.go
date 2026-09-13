package ytdlcookies

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestForURLSelectsCookiesByHost(t *testing.T) {
	SetForHost("mixcloud.com", Source{Browser: "firefox"})
	SetForHost("music.163.com", Source{File: "/tmp/netease.txt"})
	t.Cleanup(func() {
		SetForHost("mixcloud.com", Source{})
		SetForHost("music.163.com", Source{})
	})

	tests := []struct {
		url  string
		want Source
	}{
		{url: "https://www.mixcloud.com/creator/show/", want: Source{Browser: "firefox"}},
		{url: "https://music.163.com/#/song?id=1", want: Source{File: "/tmp/netease.txt"}},
		{url: "https://example.com/track", want: Source{}},
	}
	for _, test := range tests {
		if got := ForURL(test.url); got != test.want {
			t.Errorf("ForURL(%q) = %+v, want %+v", test.url, got, test.want)
		}
	}
}

func TestForURLMapsYTDLSearchPrefixes(t *testing.T) {
	SetForHost("soundcloud.com", Source{Browser: "firefox"})
	SetForHost("youtube.com", Source{Browser: "chrome"})
	t.Cleanup(func() {
		SetForHost("soundcloud.com", Source{})
		SetForHost("youtube.com", Source{})
	})

	if got := ForURL("scsearch10:ambient").Browser; got != "firefox" {
		t.Errorf("ForURL(scsearch) = %q, want firefox", got)
	}
	if got := ForURL("ytsearch10:ambient").Browser; got != "chrome" {
		t.Errorf("ForURL(ytsearch) = %q, want chrome", got)
	}
}

func TestSetForHostZeroSourceRemovesSelection(t *testing.T) {
	SetForHost("mixcloud.com", Source{Browser: "firefox"})
	SetForHost("mixcloud.com", Source{})

	if got := ForURL("https://mixcloud.com/creator/show/"); !got.IsZero() {
		t.Errorf("ForURL() = %+v after removal, want zero", got)
	}
}

func TestPrepareCopiesFileSourceAndCleansUp(t *testing.T) {
	src := filepath.Join(t.TempDir(), "cookies.txt")
	const body = "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tFALSE\t0\tk\tv\n"
	if err := os.WriteFile(src, []byte(body), 0o400); err != nil { // read-only, like a container mount
		t.Fatal(err)
	}

	args, cleanup, err := Source{Browser: "firefox", File: src}.Prepare()
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	t.Cleanup(cleanup)
	if len(args) != 2 || args[0] != "--cookies" {
		t.Fatalf("Prepare() args = %q, want --cookies <copy>", args)
	}
	if args[1] == src {
		t.Fatal("Prepare() handed out the original file; want a private copy")
	}
	copied, err := os.ReadFile(args[1])
	if err != nil || string(copied) != body {
		t.Fatalf("copy = %q, %v; want the original contents", copied, err)
	}
	// Simulate yt-dlp rewriting its jar (the copy must be writable even
	// though the source is not): the original must be untouched.
	if err := os.WriteFile(args[1], []byte("rewritten"), 0o600); err != nil {
		t.Fatal(err)
	}
	if orig, _ := os.ReadFile(src); string(orig) != body {
		t.Fatalf("original changed to %q", orig)
	}

	cleanup()
	if _, err := os.Stat(args[1]); !os.IsNotExist(err) {
		t.Fatalf("copy still present after cleanup (err=%v)", err)
	}
}

func TestPrepareNonFileSourcesNeedNoCopy(t *testing.T) {
	tests := []struct {
		name string
		src  Source
		want []string
	}{
		{"zero", Source{}, nil},
		{"browser", Source{Browser: "chrome:Profile 1"}, []string{"--cookies-from-browser", "chrome:Profile 1"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			args, cleanup, err := tc.src.Prepare()
			cleanup()
			if err != nil {
				t.Fatalf("Prepare() error = %v", err)
			}
			if !reflect.DeepEqual(args, tc.want) {
				t.Errorf("Prepare() args = %q, want %q", args, tc.want)
			}
		})
	}
}

// yt-dlp treats a missing --cookies path as an empty jar and creates the file
// on exit, so an unreadable source must fail here instead of reaching yt-dlp.
func TestPrepareUnreadableFileIsAnError(t *testing.T) {
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "locked.txt")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	// Mode 000 does not block reads on Windows or for root.
	permsEnforced := runtime.GOOS != "windows" && os.Geteuid() != 0
	tests := []struct {
		name         string
		file         string
		wantNotExist bool
		skip         bool
	}{
		{name: "missing", file: filepath.Join(dir, "nope.txt"), wantNotExist: true},
		{name: "directory", file: dir},
		{name: "unreadable", file: unreadable, skip: !permsEnforced},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("file permissions are not enforced here")
			}
			args, cleanup, err := Source{File: tc.file}.Prepare()
			cleanup()
			if err == nil {
				t.Fatalf("Prepare() = %q, nil; want an error", args)
			}
			if args != nil {
				t.Errorf("Prepare() args = %q on error, want nil", args)
			}
			if tc.wantNotExist && !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("error %v does not wrap fs.ErrNotExist", err)
			}
		})
	}
}

// A copy that cannot be created must not degrade to the original path either.
func TestPrepareTempDirFailureIsAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.TempDir on Windows does not honour TMPDIR")
	}
	src := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(src, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))
	Shutdown() // drop a session directory created under the previous TMPDIR
	t.Cleanup(Shutdown)

	args, cleanup, err := Source{File: src}.Prepare()
	cleanup()
	if err == nil || args != nil {
		t.Fatalf("Prepare() = %q, %v; want nil args and an error", args, err)
	}
}

// Copies live in one private session directory so that Shutdown can remove
// every copy at exit, including one whose process never reported its exit.
func TestShutdownRemovesSessionDirectory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	Shutdown()
	t.Cleanup(Shutdown)
	src := filepath.Join(tmp, "cookies.txt")
	if err := os.WriteFile(src, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	args, _, err := Source{File: src}.Prepare() // cleanup deliberately never called
	if err != nil {
		t.Fatal(err)
	}
	copyPath := args[1]
	sessionDir := filepath.Dir(copyPath)
	if filepath.Dir(sessionDir) != tmp {
		t.Fatalf("copy %s is not inside a session directory under TMPDIR", copyPath)
	}
	if info, err := os.Stat(sessionDir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("session directory %s: info=%v err=%v; want mode 0700", sessionDir, info, err)
	}

	Shutdown()
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Fatalf("session directory remains after Shutdown (err=%v)", err)
	}

	// Later use starts a fresh directory instead of failing.
	args, cleanup, err := Source{File: src}.Prepare()
	if err != nil {
		t.Fatalf("Prepare() after Shutdown error = %v", err)
	}
	defer cleanup()
	if filepath.Dir(args[1]) == sessionDir {
		t.Fatal("Prepare() reused the removed session directory path")
	}
}
