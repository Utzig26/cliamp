package player

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/ytdlcookies"
)

func TestYTDLCookieCopyRemovedOnNaturalExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}

	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	resetCookieDir(t)
	t.Setenv("PATH", dir)
	// No extractor output is needed: keep the decoder open after its
	// downloader exits so Close cannot be responsible for cookie cleanup.
	writeExecutable(t, filepath.Join(dir, "yt-dlp"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(dir, "ffmpeg"), "#!/bin/sh\nexec /bin/sleep 30\n")
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("# Netscape HTTP Cookie File\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	ytdlcookies.SetForHost("cookies.example", ytdlcookies.Source{File: source})
	t.Cleanup(func() { ytdlcookies.SetForHost("cookies.example", ytdlcookies.Source{}) })

	decoder, _, err := decodeYTDLPipe(context.Background(), "https://cookies.example/track", 44100, 16, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { decoder.Close() })
	select {
	case <-decoder.ytdlDone:
	case <-time.After(5 * time.Second):
		t.Fatal("yt-dlp did not finish")
	}
	if copies := cookieCopies(t, dir); len(copies) != 0 {
		t.Fatalf("cookie copies remain after yt-dlp exit, before decoder Close: %v", copies)
	}
}

func TestPlayerCloseCancelsBufferingYTDL(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX process fixtures")
	}
	for _, tc := range []struct {
		name  string
		start func(*Player) error
	}{
		{"play", func(p *Player) error {
			return p.PlayYTDLForGeneration("https://buffering.example/track", time.Minute, p.playGen.Load())
		}},
		{"preload", func(p *Player) error {
			return p.PreloadYTDLForGeneration("https://buffering.example/track", time.Minute, p.BeginPreload())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("TMPDIR", dir)
			resetCookieDir(t)
			t.Setenv("YTDL_BUFFER_DIR", dir)
			t.Setenv("PATH", dir)
			writeExecutable(t, filepath.Join(dir, "yt-dlp"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
    case "$1" in
        --cookies) shift; printf '%s\n' "$1" > "$YTDL_BUFFER_DIR/cookie" ;;
        --print) exit 1 ;;
    esac
    shift
done
printf '%s\n' "$$" > "$YTDL_BUFFER_DIR/downloader.pid"
while ! test -f "$YTDL_BUFFER_DIR/release"; do /bin/sleep 0.01; done
printf '\001\002\003\004'
`)
			writeExecutable(t, filepath.Join(dir, "ffmpeg"), `#!/bin/sh
printf '%s\n' "$$" > "$YTDL_BUFFER_DIR/ffmpeg.pid"
exec /bin/cat
`)
			source := filepath.Join(dir, "source.txt")
			if err := os.WriteFile(source, []byte("# Netscape HTTP Cookie File\n"), 0o400); err != nil {
				t.Fatal(err)
			}
			ytdlcookies.SetForHost("buffering.example", ytdlcookies.Source{File: source})
			t.Cleanup(func() { ytdlcookies.SetForHost("buffering.example", ytdlcookies.Source{}) })

			p := &Player{sr: 44100, bitDepth: 16, gapless: &gaplessStreamer{}, suspended: true}
			setupDone := make(chan struct{})
			closeDone := make(chan struct{})
			closeStarted := false
			// A broken Close must not leave the fixture or setup goroutine
			// running until the production buffering timeout.
			t.Cleanup(func() {
				for _, name := range []string{"downloader.pid", "ffmpeg.pid"} {
					contents, _ := os.ReadFile(filepath.Join(dir, name))
					if pid, err := strconv.Atoi(strings.TrimSpace(string(contents))); err == nil {
						if proc, err := os.FindProcess(pid); err == nil {
							_ = proc.Kill()
							_ = proc.Release()
						}
					}
				}
				_ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600)
				select {
				case <-setupDone:
				case <-time.After(5 * time.Second):
					t.Error("buffering setup did not finish during cleanup")
				}
				if !closeStarted {
					go func() { p.Close(); close(closeDone) }()
				}
				select {
				case <-closeDone:
				case <-time.After(5 * time.Second):
					t.Error("Close did not finish during cleanup")
				}
			})
			go func() { _ = tc.start(p); close(setupDone) }()
			readMarker := func(name string) string {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					contents, err := os.ReadFile(filepath.Join(dir, name))
					if err == nil && strings.TrimSpace(string(contents)) != "" {
						return strings.TrimSpace(string(contents))
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("timed out waiting for %s", name)
				return ""
			}
			var processes []*os.Process
			for _, name := range []string{"downloader.pid", "ffmpeg.pid"} {
				pid, err := strconv.Atoi(readMarker(name))
				if err != nil {
					t.Fatal(err)
				}
				proc, err := os.FindProcess(pid)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = proc.Release() })
				processes = append(processes, proc)
			}
			cookie := readMarker("cookie")
			if cookie == source {
				t.Fatal("downloader received the original cookie file")
			}
			if _, err := os.Stat(cookie); err != nil {
				t.Fatalf("cookie copy missing during buffering: %v", err)
			}
			select {
			case <-setupDone:
				t.Fatal("setup finished before the fixture released audio")
			default:
			}
			closeStarted = true
			go func() { p.Close(); close(closeDone) }()
			select {
			case <-closeDone:
			case <-time.After(5 * time.Second):
				t.Fatal("Close blocked on buffering playback")
			}
			if _, err := os.Stat(cookie); !os.IsNotExist(err) {
				t.Errorf("cookie copy remains after Close: %v", err)
			}
			for _, proc := range processes {
				if err := proc.Signal(syscall.Signal(0)); err == nil {
					t.Errorf("process %d was not reaped before Close returned", proc.Pid)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case <-setupDone:
			case <-time.After(5 * time.Second):
				t.Fatal("buffering setup did not return after Close")
			}
			p.mu.Lock()
			installed := p.current != nil || p.nextPipeline != nil
			p.mu.Unlock()
			if installed {
				t.Error("playback or preload was installed after Close")
			}
		})
	}
}
