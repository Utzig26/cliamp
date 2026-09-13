package player

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

// Duration requests record their PID and cookie path, then stay alive until
// canceled; playback either fails silently or sends one PCM sample through cat.
func installDurationProbeFixtures(t *testing.T, playback string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	resetCookieDir(t)
	t.Setenv("DURATION_PROBE_DIR", dir)
	t.Setenv("DURATION_PLAYBACK", playback)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), `#!/bin/sh
probe=false
cookies=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --print) probe=true ;;
        --cookies) shift; cookies=$1 ;;
    esac
    shift
done
if "$probe"; then
    printf '%s\n' "$cookies" > "$DURATION_PROBE_DIR/$$.cookie"
    exec sleep 30
fi
while ! test -f "$DURATION_PROBE_DIR/"*.cookie; do sleep 0.01; done
if [ "$DURATION_PLAYBACK" = fail ]; then exit 1; fi
printf '\001\002\003\004'
`)
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nexec cat\n")
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ytdlcookies.SetForHost("duration.example", ytdlcookies.Source{File: source})
	t.Cleanup(func() { ytdlcookies.SetForHost("duration.example", ytdlcookies.Source{}) })
	return dir
}

func waitDurationProbeFiles(t *testing.T, dir string, count int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		files, err := filepath.Glob(filepath.Join(dir, "*.cookie"))
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == count {
			ready := true
			for _, file := range files {
				contents, err := os.ReadFile(file)
				if err != nil || len(contents) == 0 {
					ready = false
					break
				}
			}
			if ready {
				return files
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d duration probes", count)
	return nil
}

func assertDurationProbesCleaned(t *testing.T, markers []string) {
	t.Helper()
	for _, marker := range markers {
		contents, err := os.ReadFile(marker)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(strings.TrimSpace(string(contents))); !os.IsNotExist(err) {
			t.Errorf("duration cookie copy remains: %s (stat: %v)", contents, err)
		}
		pid, err := strconv.Atoi(strings.TrimSuffix(filepath.Base(marker), ".cookie"))
		if err != nil {
			t.Fatal(err)
		}
		proc, err := os.FindProcess(pid)
		if err == nil {
			if err := proc.Signal(syscall.Signal(0)); err == nil {
				t.Errorf("duration process %d remains alive after cleanup", pid)
			}
			proc.Release()
		}
	}
}

func TestYTDLDurationProbeCanceledWhenResultAbandoned(t *testing.T) {
	for _, playback := range []string{"fail", "ok"} {
		t.Run(playback, func(t *testing.T) {
			dir := installDurationProbeFixtures(t, playback)
			p := &Player{sr: 44100, bitDepth: 16, gapless: &gaplessStreamer{}, suspended: true}
			t.Cleanup(p.Close)
			// Reject the successful pipeline as stale to avoid opening an audio
			// device. Duration collection still follows the production path.
			p.playGen.Store(2)
			started := time.Now()
			err := p.PlayYTDLForGeneration("https://duration.example/track", 0, 1)
			if (err != nil) != (playback == "fail") {
				t.Fatalf("PlayYTDLForGeneration() = %v", err)
			}
			if elapsed := time.Since(started); elapsed > 5*time.Second {
				t.Fatalf("playback waited for the abandoned probe: %v", elapsed)
			}
			markers := waitDurationProbeFiles(t, dir, 1)
			// Observe cancellation without invoking Close (which could mask a
			// missing cancellation at setup failure or the two-second deadline).
			done := make(chan struct{})
			go func() { p.ytdl.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("abandoned duration probe was not canceled")
			}
			assertDurationProbesCleaned(t, markers)
		})
	}
}

func TestPlayerCloseWaitsForDurationProbeCleanup(t *testing.T) {
	dir := installDurationProbeFixtures(t, "fail")
	p := &Player{gapless: &gaplessStreamer{}, suspended: true}
	t.Cleanup(p.Close)
	// Also reap fixtures if a regression disconnects Close from the probe owner.
	t.Cleanup(p.ytdl.Close)
	for i := 0; i < 2; i++ {
		_, cancel, err := p.startDurationProbe("https://duration.example/track")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(cancel)
	}
	markers := waitDurationProbeFiles(t, dir, 2)
	p.Close()
	assertDurationProbesCleaned(t, markers)
	p.Close()
	if _, _, err := p.startDurationProbe("https://duration.example/track"); err == nil {
		t.Fatal("probe started after Close")
	}
}

func TestYTDLDurationProbeRegistrationRacesShutdown(t *testing.T) {
	dir := installDurationProbeFixtures(t, "fail")
	p := &Player{}
	t.Cleanup(p.ytdl.Close)
	_, cancel, err := p.startDurationProbe("https://duration.example/track")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cancel)
	// Keep a registered probe alive while other registrations race shutdown.
	markers := waitDurationProbeFiles(t, dir, 1)
	var callers sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			if _, cancel, err := p.startDurationProbe("https://duration.example/track"); err == nil {
				cancel()
			}
		}()
	}
	close(start)
	p.ytdl.Close()
	callers.Wait()
	assertDurationProbesCleaned(t, markers)
	if left := cookieCopies(t, dir); len(left) != 0 {
		t.Fatalf("cookie copies after shutdown: %v", left)
	}
}

func TestYTDLDurationProbeResult(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixture")
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	resetCookieDir(t)
	t.Setenv("PATH", dir)
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\nprintf '12.5\\n'\n")
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("# Netscape HTTP Cookie File\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ytdlcookies.SetForHost("duration.example", ytdlcookies.Source{File: source})
	t.Cleanup(func() { ytdlcookies.SetForHost("duration.example", ytdlcookies.Source{}) })

	got := probeYTDLDuration(context.Background(), "https://duration.example/track")
	if got != 12500*time.Millisecond {
		t.Fatalf("duration = %v, want 12.5s", got)
	}
	if left := cookieCopies(t, dir); len(left) != 0 {
		t.Fatalf("cookie copies after result: %v", left)
	}
}
