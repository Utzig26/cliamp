package model

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/playlist"
)

// A stream can commit in the real engine before its command result reaches
// Update. A failed retry must retain that stream, even for duplicate paths
// and regardless of the order in which the two results arrive.
func TestCommittedStreamSurvivesFailedRetry(t *testing.T) {
	if sharedPlayer == nil {
		t.Skip("audio hardware unavailable")
	}
	aPath := filepath.Join(t.TempDir(), "a.wav")
	if err := os.WriteFile(aPath, ownershipTestWAV(30), 0o600); err != nil {
		t.Fatal(err)
	}
	bAudio := ownershipTestWAV(60)
	for _, failureFirst := range []bool{false, true} {
		name := "success result first"
		if failureFirst {
			name = "failure result first"
		}
		t.Run(name, func(t *testing.T) {
			engine := sharedPlayer
			engine.Stop()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if requests.Add(1) > 1 {
					http.Error(w, "retry refused", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "audio/wav")
				_, _ = w.Write(bAudio)
			}))
			defer server.Close()
			defer engine.Stop()
			a := playlist.Track{Path: aPath, Title: "A", DurationSecs: 30}
			b := playlist.Track{Path: server.URL + "/b.wav", Title: "B", Stream: true, DurationSecs: 60}
			pl := playlist.New()
			pl.Add(a, b, b)
			pl.SetIndex(0)
			m := Model{player: engine, playlist: pl}
			m.playTrack(a)
			if m.err != nil || engine.Duration() != 30*time.Second {
				t.Fatalf("start A: error=%v duration=%v", m.err, engine.Duration())
			}
			success := m.nextTrack()().(streamPlayedMsg)
			if success.err != nil || engine.Duration() != 60*time.Second {
				t.Fatalf("start B: error=%v duration=%v", success.err, engine.Duration())
			}
			failure := m.nextTrack()().(streamPlayedMsg)
			if failure.err == nil {
				t.Fatal("retry unexpectedly succeeded")
			}
			results := []streamPlayedMsg{success, failure}
			if failureFirst {
				results[0], results[1] = results[1], results[0]
			}
			for _, result := range results {
				updated, _ := m.Update(result)
				m = updated.(Model)
			}
			if !engine.IsPlaying() || engine.Duration() != 60*time.Second || requests.Load() != 2 {
				t.Fatalf("engine playing=%t duration=%v requests=%d", engine.IsPlaying(), engine.Duration(), requests.Load())
			}
			if got, _ := m.currentPlaybackTrack(); got.Path != b.Path {
				t.Fatalf("engine retained B but model reports %q", got.Title)
			}
			if m.buffering || m.requestedTrackActive || !errors.Is(m.err, failure.err) {
				t.Fatalf("retry did not settle: buffering=%t pending=%t error=%v", m.buffering, m.requestedTrackActive, m.err)
			}
		})
	}
}

// ownershipTestWAV produces silent stereo PCM with a distinct decoded duration,
// allowing the real engine's active pipeline to be identified independently of
// the model's track metadata.
func ownershipTestWAV(seconds int) []byte {
	data := make([]byte, 44+seconds*44100*4)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 2)
	binary.LittleEndian.PutUint32(data[24:], 44100)
	binary.LittleEndian.PutUint32(data[28:], 44100*4)
	binary.LittleEndian.PutUint16(data[32:], 4)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	return data
}

func TestDelayedStreamResultKeepsCommittedOwner(t *testing.T) {
	for _, path := range []string{"https://example.com/b.mp3", "https://www.youtube.com/watch?v=b"} {
		for _, started := range []bool{false, true} {
			name := path + "/superseded before start"
			if started {
				name = path + "/started before superseded"
			}
			t.Run(name, func(t *testing.T) {
				a := playlist.Track{Path: "/music/a.flac", Title: "A"}
				b := playlist.Track{Path: path, Title: "B", Stream: true}
				pl := playlist.New()
				pl.Add(a, b, b)
				pl.SetIndex(0)
				engine := &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}
				m := Model{player: engine, playlist: pl}
				m.setPlaybackTrack(a)
				first := m.nextTrack()
				var result streamPlayedMsg
				if started {
					result = first().(streamPlayedMsg)
				}
				retry := m.nextTrack()
				if !started {
					result = first().(streamPlayedMsg)
				}
				engine.startErr = errors.New("retry refused")
				updated, _ := m.Update(result)
				m = updated.(Model)
				if !m.buffering || !m.requestedTrackActive {
					t.Fatal("stale result settled the pending retry")
				}
				updated, _ = m.Update(retry())
				m = updated.(Model)
				want := a.Path
				if started {
					want = b.Path
				}
				if got, _ := m.currentPlaybackTrack(); got.Path != want {
					t.Fatalf("current track = %q, want %q", got.Path, want)
				}
			})
		}
	}
}

// setPlaybackTrack records track as engine-owned in one step, for tests that
// start from a playing state.
func (m *Model) setPlaybackTrack(track playlist.Track) {
	m.requestPlaybackTrack(track)
	m.commitPlaybackTrack()
}

// scrobbleProv records which track was scrobbled.
type scrobbleProv struct {
	nowPlayingProv
	scrobbles chan playlist.Track
}

func (p *scrobbleProv) ReportScrobble(t playlist.Track, _, _ time.Duration, _ bool) error {
	p.scrobbles <- t
	return nil
}

// startNext advances the playlist and starts the new current track, delivering
// the asynchronous stream result when the start is a stream.
func startNext(t *testing.T, m *Model) {
	t.Helper()
	cmd := m.nextTrack()
	track, _ := m.playlist.Current()
	if !playlist.IsURL(track.Path) {
		return
	}
	msg, ok := cmd().(streamPlayedMsg)
	if !ok {
		t.Fatal("playback command did not return streamPlayedMsg")
	}
	updated, _ := m.Update(msg)
	*m = updated.(Model)
}

// TestStartOutcomeDecidesPlaybackOwner checks that the engine-owned track only
// changes when a start succeeds. The engine builds a replacement before
// swapping sources, so a failed start leaves the previous track playing.
func TestStartOutcomeDecidesPlaybackOwner(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	local := playlist.Track{Path: "/music/b.flac", Title: "B"}
	stream := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Title: "B"}
	startErr := errors.New("playback startup failed")
	tests := []struct {
		name      string
		engine    *nowPlayingEngine
		owned     bool // a is playing when the next start is issued
		next      playlist.Track
		wantOwner string // path of the engine-owned track afterwards, "" for none
	}{
		{
			name:      "local start fails while a plays",
			engine:    &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}, startErr: startErr},
			owned:     true,
			next:      local,
			wantOwner: a.Path,
		},
		{
			name:      "stream start fails while a plays",
			engine:    &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}, startErr: startErr},
			owned:     true,
			next:      stream,
			wantOwner: a.Path,
		},
		{
			name:      "local start fails while a is paused",
			engine:    &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true, paused: true}, startErr: startErr},
			owned:     true,
			next:      local,
			wantOwner: a.Path,
		},
		{
			name:   "local start fails with nothing playing",
			engine: &nowPlayingEngine{startErr: startErr},
			next:   local,
		},
		{
			name:   "stream start fails with nothing playing",
			engine: &nowPlayingEngine{startErr: startErr},
			next:   stream,
		},
		{
			name:      "local start succeeds",
			engine:    &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}},
			owned:     true,
			next:      local,
			wantOwner: local.Path,
		},
		{
			name:      "stream start succeeds",
			engine:    &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}},
			owned:     true,
			next:      stream,
			wantOwner: stream.Path,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pl := playlist.New()
			pl.Add(a, tt.next)
			pl.SetIndex(0)
			m := Model{player: tt.engine, playlist: pl}
			if tt.owned {
				m.setPlaybackTrack(a)
			}
			startNext(t, &m)

			if tt.engine.startErr != nil && !errors.Is(m.err, tt.engine.startErr) {
				t.Fatalf("playback error = %v, want %v", m.err, tt.engine.startErr)
			}
			if m.requestedTrackActive {
				t.Errorf("start request still pending for %q", m.requestedTrack.Path)
			}
			if m.buffering {
				t.Error("still buffering after the start settled")
			}
			if got := ownedPath(m); got != tt.wantOwner {
				t.Errorf("engine-owned track = %q, want %q", got, tt.wantOwner)
			}
			wantCurrent := tt.wantOwner
			if wantCurrent == "" {
				wantCurrent = tt.next.Path // playlist fallback
			}
			if got, _ := m.currentPlaybackTrack(); got.Path != wantCurrent {
				t.Errorf("currentPlaybackTrack() = %q, want %q", got.Path, wantCurrent)
			}
		})
	}
}

func ownedPath(m Model) string {
	if !m.playingTrackActive {
		return ""
	}
	return m.playingTrack.Path
}

// TestStreamStartIsRequestedBeforeOwned checks that a buffering stream is
// what playback is about, while the engine still owns the previous track.
func TestStreamStartIsRequestedBeforeOwned(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Title: "B"}
	pl := playlist.New()
	pl.Add(a, b)
	pl.SetIndex(0)
	m := Model{player: &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}, playlist: pl}
	m.setPlaybackTrack(a)

	cmd := m.nextTrack()
	if !m.buffering {
		t.Fatal("stream start did not enter buffering")
	}
	if got, _ := m.currentPlaybackTrack(); got.Path != b.Path {
		t.Fatalf("currentPlaybackTrack() while buffering = %q, want %q", got.Path, b.Path)
	}
	if got := ownedPath(m); got != a.Path {
		t.Fatalf("engine-owned track while buffering = %q, want %q", got, a.Path)
	}

	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if got := ownedPath(m); got != b.Path {
		t.Fatalf("engine-owned track after start = %q, want %q", got, b.Path)
	}
}

// TestStaleStreamResultKeepsOwner checks that a start superseded by a newer
// request cannot take ownership when its result arrives late.
func TestStaleStreamResultKeepsOwner(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Title: "B"}
	c := playlist.Track{Path: "https://www.youtube.com/watch?v=c", Title: "C"}
	pl := playlist.New()
	pl.Add(a, b, c)
	pl.SetIndex(0)
	m := Model{player: &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}, playlist: pl}
	m.setPlaybackTrack(a)

	staleCmd := m.nextTrack()
	currentCmd := m.nextTrack()

	updated, _ := m.Update(staleCmd())
	m = updated.(Model)
	if got := ownedPath(m); got != a.Path {
		t.Fatalf("engine-owned track after stale result = %q, want %q", got, a.Path)
	}
	if !m.buffering || !m.requestedTrackActive || m.requestedTrack.Path != c.Path {
		t.Fatalf("stale result disturbed the pending start: buffering=%t requested=%q", m.buffering, m.requestedTrack.Path)
	}

	updated, _ = m.Update(currentCmd())
	m = updated.(Model)
	if got := ownedPath(m); got != c.Path {
		t.Fatalf("engine-owned track after current result = %q, want %q", got, c.Path)
	}
}

// TestGaplessAdvanceOwnsNextTrack checks that a gapless boundary, where the
// engine already switched sources, hands ownership over immediately.
func TestGaplessAdvanceOwnsNextTrack(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A", DurationSecs: 180}
	b := playlist.Track{Path: "/music/b.flac", Title: "B", DurationSecs: 180}
	pl := playlist.New()
	pl.Add(a, b)
	pl.SetIndex(0)
	m := Model{player: &playbackFakeEngine{playing: true, gaplessAdvanced: true}, playlist: pl}
	m.setPlaybackTrack(a)

	updated, _ := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	if got := ownedPath(m); got != b.Path {
		t.Fatalf("engine-owned track after gapless advance = %q, want %q", got, b.Path)
	}
	if m.requestedTrackActive {
		t.Fatal("gapless advance left a start request pending")
	}
}

// TestDetachWhileBufferingCommitsDetached checks that replacing the playlist
// under a buffering start leaves the started track detached from the playlist.
func TestDetachWhileBufferingCommitsDetached(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Title: "B"}
	other := playlist.Track{Path: "/music/other.flac", Title: "Other"}
	pl := playlist.New()
	pl.Add(a, b)
	pl.SetIndex(0)
	m := Model{player: &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}, playlist: pl}
	m.setPlaybackTrack(a)

	cmd := m.nextTrack()
	m.detachPlaybackTrack()
	m.replacePlaylist([]playlist.Track{other})

	updated, _ := m.Update(cmd())
	m = updated.(Model)
	if got := ownedPath(m); got != b.Path {
		t.Fatalf("engine-owned track = %q, want %q", got, b.Path)
	}
	if !m.playbackDetached {
		t.Fatal("started track is not detached from the replaced playlist")
	}
}

// TestDrainAfterFailedStartScrobblesTrackThatPlayed checks that when a start
// fails and the previous track plays to its end, the track that actually
// played is the one scrobbled.
func TestDrainAfterFailedStartScrobblesTrackThatPlayed(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A", DurationSecs: 120}
	b := playlist.Track{Path: "/music/b.flac", Title: "B", DurationSecs: 120}
	startErr := errors.New("playback startup failed")
	engine := &nowPlayingEngine{
		playbackFakeEngine: playbackFakeEngine{playing: true, duration: 120 * time.Second},
		startErr:           startErr,
	}
	prov := &scrobbleProv{
		nowPlayingProv: nowPlayingProv{reports: make(chan playlist.Track, 4)},
		scrobbles:      make(chan playlist.Track, 4),
	}
	pl := playlist.New()
	pl.Add(a, b)
	pl.SetIndex(0)
	m := Model{
		player:    engine,
		playlist:  pl,
		providers: []ProviderEntry{{Key: "p", Name: "P", Provider: prov}},
	}
	m.setPlaybackTrack(a)

	startNext(t, &m)
	if !errors.Is(m.err, startErr) {
		t.Fatalf("playback error = %v, want %v", m.err, startErr)
	}

	engine.drained = true
	updated, _ := m.Update(tickMsg(time.Now()))
	m = updated.(Model)
	select {
	case got := <-prov.scrobbles:
		if got.Path != a.Path {
			t.Fatalf("scrobbled %q, want the track that played, %q", got.Path, a.Path)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no scrobble reported for the drained track")
	}
}
