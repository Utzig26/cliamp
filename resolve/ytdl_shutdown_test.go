package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/workgroup"
	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

// readStartMarker returns the PID and --cookies path recorded by the fake
// yt-dlp, or nil and "" while it has not started.
func readStartMarker(marker string) (*os.Process, string) {
	data, err := os.ReadFile(marker)
	if err != nil {
		return nil, ""
	}
	fields := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(fields) != 2 {
		return nil, ""
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 0 {
		return nil, ""
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, ""
	}
	return proc, fields[1]
}

func TestShutdownYTDLCancelsRequestsAndRemovesCookieCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	// Record PID and private cookie path, then block until killed.
	script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--cookies" ]; then shift; cookie=$1; fi
  shift
done
printf '%s\n%s\n' "$$" "$cookie" > "$RESOLVE_SHUTDOWN_MARKER"
exec /bin/sleep 60
`
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RESOLVE_SHUTDOWN_MARKER", marker)
	t.Setenv("TMPDIR", dir)
	ytdlcookies.Shutdown()
	t.Cleanup(ytdlcookies.Shutdown)

	source := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(source, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := ytdlcookies.Source{File: source}
	ytdlcookies.SetForHost("shutdown.example", src)
	t.Cleanup(func() { ytdlcookies.SetForHost("shutdown.example", ytdlcookies.Source{}) })
	// ShutdownYTDL is terminal for the package-level group; give later tests a fresh one.
	t.Cleanup(func() { pendingYTDL = workgroup.Group{} })

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"batch", func() error {
			_, err := ResolveYTDLBatch("https://shutdown.example/playlist", 0, 0, ytdlcookies.Source{})
			return err
		}},
		{"playlists", func() error {
			_, err := FetchUserPlaylists(src)
			return err
		}},
		{"download", func() error {
			_, err := DownloadYTDL("https://shutdown.example/track", t.TempDir())
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			os.Remove(marker)
			pendingYTDL = workgroup.Group{}
			t.Cleanup(func() {
				if proc, _ := readStartMarker(marker); proc != nil {
					_ = proc.Kill()
					_ = proc.Release()
				}
			})

			done := make(chan error, 1)
			go func() { done <- tc.run() }()
			var (
				proc       *os.Process
				cookieCopy string
			)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if proc, cookieCopy = readStartMarker(marker); proc != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if proc == nil {
				t.Fatal("yt-dlp did not start")
			}
			defer proc.Release()
			if cookieCopy == source {
				t.Fatal("yt-dlp was handed the original cookies file")
			}
			if _, err := os.Stat(cookieCopy); err != nil {
				t.Fatalf("private cookie copy while yt-dlp runs: %v", err)
			}

			ShutdownYTDL()
			if _, err := os.Stat(cookieCopy); !os.IsNotExist(err) {
				t.Errorf("private cookie copy after shutdown: %v", err)
			}
			if err := proc.Signal(syscall.Signal(0)); err == nil {
				t.Error("yt-dlp is still alive after shutdown")
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("cancelled request returned no error")
				}
			case <-time.After(time.Second):
				t.Fatal("request did not return after shutdown")
			}
			if err := tc.run(); !errors.Is(err, workgroup.ErrClosed) {
				t.Errorf("request after shutdown error = %v, want ErrClosed", err)
			}
		})
	}
}
