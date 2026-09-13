package model

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bjarneo/cliamp/history"
	"github.com/bjarneo/cliamp/lyrics"
	"github.com/bjarneo/cliamp/playlist"
)

// trackStartedModel returns a model playing a with lyrics shown, an ICY title,
// a history store, and a resume saver, so a test can see which of those a
// later start touches.
func trackStartedModel(t *testing.T, engine *nowPlayingEngine, next playlist.Track) (Model, *[]playlist.Track) {
	t.Helper()
	a := playlist.Track{Path: "/music/a.flac", Artist: "Artist", Title: "A"}
	pl := playlist.New()
	pl.Add(a, next)
	pl.SetIndex(0)
	m := Model{player: engine, playlist: pl}
	m.setPlaybackTrack(a)
	// Installed after a started, so only later starts leave a trace.
	m.historyStore = history.NewAt(filepath.Join(t.TempDir(), "history.toml"))
	saved := &[]playlist.Track{}
	m.SetResumeSaver(func(track playlist.Track, _ int, _ []playlist.Track, _ int) {
		*saved = append(*saved, track)
	})
	m.lyrics = lyricsState{visible: true, lines: []lyrics.Line{{Text: "a's line"}}, query: "Artist\nA"}
	m.streamTitle = "A live"
	return m, saved
}

func historyPaths(t *testing.T, m Model) []string {
	t.Helper()
	entries, err := m.historyStore.Recent(10)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		paths = append(paths, e.Track.Path)
	}
	return paths
}

// TestFailedStartLeavesPreviousTrackStateAlone checks that a start which fails
// while another track plays does not touch that track's lyrics, ICY title,
// history, or resume checkpoint.
func TestFailedStartLeavesPreviousTrackStateAlone(t *testing.T) {
	b := playlist.Track{Path: "/music/b.flac", Artist: "Artist", Title: "B"}
	engine := &nowPlayingEngine{
		playbackFakeEngine: playbackFakeEngine{playing: true},
		startErr:           errors.New("playback startup failed"),
	}
	m, saved := trackStartedModel(t, engine, b)

	m.nextTrack()
	if !errors.Is(m.err, engine.startErr) {
		t.Fatalf("playback error = %v, want %v", m.err, engine.startErr)
	}
	if len(m.lyrics.lines) != 1 || m.lyrics.loading || m.lyrics.query != "Artist\nA" {
		t.Errorf("lyrics = %+v, want a's lyrics untouched", m.lyrics)
	}
	if m.streamTitle != "A live" {
		t.Errorf("stream title = %q, want a's title kept", m.streamTitle)
	}
	if paths := historyPaths(t, m); len(paths) != 0 {
		t.Errorf("history = %v, want no entry for a track that never played", paths)
	}
	if len(*saved) != 0 {
		t.Errorf("resume checkpoints = %v, want none for a failed start", *saved)
	}
}

// TestStreamStartAppliesTrackStateOnCommit checks that a buffering stream
// leaves the playing track's state alone until the engine switches, and that
// the switch then records history, checkpoints, and refreshes lyrics.
func TestStreamStartAppliesTrackStateOnCommit(t *testing.T) {
	b := playlist.Track{Path: "https://www.youtube.com/watch?v=b", Artist: "Artist", Title: "B"}
	engine := &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}
	m, saved := trackStartedModel(t, engine, b)

	cmd := m.nextTrack()
	if len(m.lyrics.lines) != 1 || m.lyrics.loading || m.streamTitle != "A live" {
		t.Fatalf("buffering start changed the playing track's state: lyrics %+v title %q", m.lyrics, m.streamTitle)
	}
	if paths := historyPaths(t, m); len(paths) != 0 {
		t.Fatalf("history = %v before the start settled, want empty", paths)
	}

	updated, startedCmd := m.Update(cmd())
	m = updated.(Model)
	if startedCmd == nil {
		t.Fatal("stream start returned no command, want the lyrics fetch")
	}
	if len(m.lyrics.lines) != 0 || !m.lyrics.loading || m.lyrics.query != "Artist\nB" {
		t.Errorf("lyrics = %+v, want b's fetch in flight", m.lyrics)
	}
	if m.streamTitle != "" {
		t.Errorf("stream title = %q, want cleared for the new track", m.streamTitle)
	}
	if paths := historyPaths(t, m); len(paths) != 1 || paths[0] != b.Path {
		t.Errorf("history = %v, want [%s]", paths, b.Path)
	}
	if len(*saved) != 1 || (*saved)[0].Path != b.Path {
		t.Errorf("resume checkpoints = %v, want one for %s", *saved, b.Path)
	}
}

// TestLocalStartAppliesTrackStateOnCommit checks the synchronous path.
func TestLocalStartAppliesTrackStateOnCommit(t *testing.T) {
	b := playlist.Track{Path: "/music/b.flac", Artist: "Artist", Title: "B"}
	engine := &nowPlayingEngine{playbackFakeEngine: playbackFakeEngine{playing: true}}
	m, saved := trackStartedModel(t, engine, b)

	if cmd := m.nextTrack(); cmd == nil {
		t.Fatal("local start returned no command, want the lyrics fetch")
	}
	if !m.lyrics.loading || m.lyrics.query != "Artist\nB" {
		t.Errorf("lyrics = %+v, want b's fetch in flight", m.lyrics)
	}
	if paths := historyPaths(t, m); len(paths) != 1 || paths[0] != b.Path {
		t.Errorf("history = %v, want [%s]", paths, b.Path)
	}
	if len(*saved) != 1 || (*saved)[0].Path != b.Path {
		t.Errorf("resume checkpoints = %v, want one for %s", *saved, b.Path)
	}
}
