package ytmusic_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/external/ytmusic"
	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

func TestCookieProvidersCloseJoinsResolvers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping Unix shell script test on Windows")
	}
	playlists := func(p *ytmusic.CookieProvider) error {
		_, err := p.Playlists()
		return err
	}
	tracks := func(p *ytmusic.CookieProvider) error {
		_, err := p.Tracks("PL_shutdown_test")
		return err
	}
	search := func(p *ytmusic.CookieProvider) error {
		_, err := p.SearchTracks(context.Background(), "shutdown test", 1)
		return err
	}
	for _, tc := range []struct {
		name string
		pick func(ytmusic.CookieProviders) *ytmusic.CookieProvider
		run  func(*ytmusic.CookieProvider) error
	}{
		{"music playlists", func(p ytmusic.CookieProviders) *ytmusic.CookieProvider { return p.Music }, playlists},
		{"shared video tracks", func(p ytmusic.CookieProviders) *ytmusic.CookieProvider { return p.Video }, tracks},
		{"shared all search", func(p ytmusic.CookieProviders) *ytmusic.CookieProvider { return p.All }, search},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			privateDir := t.TempDir()
			marker := filepath.Join(dir, "started")
			source := filepath.Join(dir, "cookies.txt")
			if err := os.WriteFile(source, []byte("# Netscape HTTP Cookie File\n.youtube.com\tTRUE\t/\tTRUE\t0\ttest\tsynthetic\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
while [ "$#" -gt 0 ]; do
    if [ "$1" = "--cookies" ]; then
        shift
        cookie="$1"
    fi
    shift
done
[ -f "$cookie" ] || exit 2
printf '%s\t%s\n' "$$" "$cookie" >> "$CLIAMP_SHUTDOWN_MARKER"
exec /bin/sleep 30
`
			if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TMPDIR", privateDir)
			ytdlcookies.Shutdown() // start the session directory under privateDir
			t.Cleanup(ytdlcookies.Shutdown)
			t.Setenv("CLIAMP_SHUTDOWN_MARKER", marker)

			provs := ytmusic.NewCookieProviders(ytdlcookies.Source{File: source})
			var requests []<-chan error
			start := func(run func() error) <-chan error {
				done := make(chan error, 1)
				requests = append(requests, done)
				go func() { done <- run(); close(done) }()
				return done
			}
			// Kill fake processes even if Close regresses, then bound all joins.
			t.Cleanup(func() {
				data, _ := os.ReadFile(marker)
				for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
					fields := strings.SplitN(line, "\t", 2)
					if pid, err := strconv.Atoi(fields[0]); err == nil {
						if proc, err := os.FindProcess(pid); err == nil {
							_ = proc.Kill()
							_ = proc.Release()
						}
					}
				}
				for _, done := range requests {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("resolver did not finish after cleanup killed fake yt-dlp")
					}
				}
			})
			done := start(func() error { return tc.run(tc.pick(provs)) })
			var fields []string
			deadline := time.Now().Add(3 * time.Second)
			for time.Now().Before(deadline) {
				data, _ := os.ReadFile(marker)
				if strings.HasSuffix(string(data), "\n") {
					fields = strings.SplitN(strings.TrimSpace(string(data)), "\t", 2)
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if len(fields) != 2 {
				t.Fatal("fake yt-dlp did not start with a private cookie copy")
			}
			pid, err := strconv.Atoi(fields[0])
			if err != nil {
				t.Fatal(err)
			}
			if fields[1] == source {
				t.Fatal("yt-dlp received original cookie file")
			}

			closed := start(func() error { provs.Music.Close(); return nil })
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("Close did not promptly cancel and join active resolver")
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			defer proc.Release()
			if err := proc.Signal(syscall.Signal(0)); err == nil {
				t.Errorf("yt-dlp process still exists at Close return: %v", err)
			}
			if _, err := os.Stat(fields[1]); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("private cookie copy still exists at Close return: %v", err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("canceled resolver returned no error")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("resolver did not return after Close")
			}

			denied := start(func() error { return tc.run(tc.pick(provs)) })
			select {
			case err := <-denied:
				if err == nil {
					t.Error("resolver accepted request after Close")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("resolver started work after Close")
			}
			data, err := os.ReadFile(marker)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(data), "\n") != 1 {
				t.Error("another yt-dlp process started after Close")
			}
			copies, err := filepath.Glob(filepath.Join(privateDir, "cliamp-cookies-*", "*"))
			if err != nil || len(copies) != 0 {
				t.Errorf("private cookie copies after Close: %v, err=%v", copies, err)
			}
		})
	}
}
