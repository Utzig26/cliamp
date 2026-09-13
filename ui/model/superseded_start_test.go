package model

import (
	"errors"
	"testing"

	"github.com/bjarneo/cliamp/playlist"
)

// supersededStartModel returns a model playing a, with b's stream start
// already committed by the engine but its result not yet delivered.
func supersededStartModel(t *testing.T) (Model, *nowPlayingEngine, streamPlayedMsg, playlist.Track) {
	t.Helper()
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Title: "B"}
	c := playlist.Track{Path: "/music/c.flac", Title: "C"}
	pl := playlist.New()
	pl.Add(a, b, c)
	pl.SetIndex(0)
	engine := &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}
	m := Model{player: engine, playlist: pl}
	m.setPlaybackTrack(a)

	cmd := m.nextTrack()
	msg, ok := cmd().(streamPlayedMsg) // the engine switches to b here
	if !ok || msg.err != nil {
		t.Fatalf("stream start = %T err %v, want a committed streamPlayedMsg", msg, msg.err)
	}
	return m, engine, msg, b
}

// TestSupersededCommittedStartBecomesOwner covers a start the engine switched
// to before a newer request replaced it: when that newer request fails, the
// superseded track is the one still playing.
func TestSupersededCommittedStartBecomesOwner(t *testing.T) {
	m, engine, msgB, b := supersededStartModel(t)
	engine.startErr = errors.New("playback startup failed")
	if m.nextTrack() != nil || !errors.Is(m.err, engine.startErr) {
		t.Fatalf("newer local start did not fail synchronously: err %v", m.err)
	}

	updated, _ := m.Update(msgB)
	m = updated.(Model)
	if got := ownedPath(m); got != b.Path {
		t.Fatalf("engine-owned track = %q, want the superseded start %q", got, b.Path)
	}
	if got, _ := m.currentPlaybackTrack(); got.Path != b.Path {
		t.Fatalf("currentPlaybackTrack() = %q, want %q", got.Path, b.Path)
	}
}

// TestSupersededCommittedStartYieldsToNewerOwner checks that the late result
// cannot displace a start that settled after it.
func TestSupersededCommittedStartYieldsToNewerOwner(t *testing.T) {
	m, _, msgB, _ := supersededStartModel(t)
	if m.nextTrack() != nil || m.err != nil {
		t.Fatalf("newer local start did not succeed: err %v", m.err)
	}
	c, _ := m.playlist.Current()

	updated, _ := m.Update(msgB)
	m = updated.(Model)
	if got := ownedPath(m); got != c.Path {
		t.Fatalf("engine-owned track = %q, want the newer start %q", got, c.Path)
	}
}

// TestSupersededCommittedStartAfterStopIsIgnored checks that a stop between
// the engine's switch and the result leaves nothing owned.
func TestSupersededCommittedStartAfterStopIsIgnored(t *testing.T) {
	m, _, msgB, _ := supersededStartModel(t)
	m.stopPlayback()

	updated, _ := m.Update(msgB)
	m = updated.(Model)
	if got := ownedPath(m); got != "" {
		t.Fatalf("engine-owned track = %q after stop, want none", got)
	}
}

// TestRefusedStartChangesNothing checks that a start the engine refused, which
// it reports as superseded, leaves ownership and the pending request alone.
func TestRefusedStartChangesNothing(t *testing.T) {
	a := playlist.Track{Path: "/music/a.flac", Title: "A"}
	b := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Title: "B"}
	c := playlist.Track{Path: "https://www.youtube.com/watch?v=c", Title: "C"}
	pl := playlist.New()
	pl.Add(a, b, c)
	pl.SetIndex(0)
	m := Model{player: &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}, playlist: pl}
	m.setPlaybackTrack(a)

	refusedCmd := m.nextTrack()
	m.nextTrack()
	msg := refusedCmd().(streamPlayedMsg)
	if msg.err == nil {
		t.Fatal("engine did not report the older start as superseded")
	}

	updated, _ := m.Update(msg)
	m = updated.(Model)
	if got := ownedPath(m); got != a.Path {
		t.Fatalf("engine-owned track = %q, want %q", got, a.Path)
	}
	if !m.requestedTrackActive || m.requestedTrack.Path != c.Path {
		t.Fatalf("pending request = %q, want %q", m.requestedTrack.Path, c.Path)
	}
}
