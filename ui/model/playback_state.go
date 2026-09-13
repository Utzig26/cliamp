package model

import "github.com/bjarneo/cliamp/playlist"

// currentPlaybackTrack returns the track playback is about: the one a start
// is buffering for, else the one the engine owns while it plays, else the
// playlist's current track. A negative index means there is no such track.
func (m Model) currentPlaybackTrack() (playlist.Track, int) {
	if m.buffering && m.requestedTrackActive {
		return m.requestedTrack, 0
	}
	if m.playingTrackActive && m.player != nil && m.player.IsPlaying() {
		return m.playingTrack, 0
	}
	if m.playlist == nil {
		return playlist.Track{}, -1
	}
	return m.playlist.Current()
}

func (m Model) currentPlaybackIsLive(track playlist.Track) bool {
	if track.IsLive() {
		return true
	}
	reporter, ok := m.player.(interface{ IsLiveStream() bool })
	return ok && reporter.IsLiveStream()
}

// requestPlaybackTrack records the track a start was issued for. Ownership
// moves to it only once the engine has it (commitPlaybackTrack), because a
// start that fails leaves whatever was playing untouched.
func (m *Model) requestPlaybackTrack(track playlist.Track) {
	m.requestedTrack = track
	m.requestedTrackActive = true
	m.requestedTrackGen = m.requests.stream
	m.playbackDetached = false
}

// commitPlaybackTrack makes the requested track the engine-owned one.
func (m *Model) commitPlaybackTrack() {
	m.playingTrack = m.requestedTrack
	m.playingTrackActive = true
	m.playingTrackGen = m.requestedTrackGen
	m.requestedTrack = playlist.Track{}
	m.requestedTrackActive = false
}

// adoptSupersededStart handles the result of a start that a newer request
// replaced before it settled. A nil error means the engine had already
// switched to that track, and it keeps playing until the newer request lands,
// so it becomes the owner unless a later start already did or playback
// stopped since. A refused or failed start changed nothing.
func (m *Model) adoptSupersededStart(msg streamPlayedMsg) {
	if msg.err != nil || msg.gen <= m.playingTrackGen || m.player == nil || !m.player.IsPlaying() {
		return
	}
	m.playingTrack = msg.track
	m.playingTrackActive = true
	m.playingTrackGen = msg.gen
}

// failPlaybackTrack drops the requested track after its start failed. The
// previous owner keeps playing when the engine still has it, paused or not;
// otherwise nothing is owned.
func (m *Model) failPlaybackTrack() {
	m.requestedTrack = playlist.Track{}
	m.requestedTrackActive = false
	if m.player == nil || !m.player.IsPlaying() {
		m.clearPlaybackTrack()
	}
}

// detachPlaybackTrack marks playback as no longer belonging to the playlist,
// after the playlist was replaced under a playing or starting track. A start
// still pending commits into the detached state.
func (m *Model) detachPlaybackTrack() {
	if !m.requestedTrackActive && !m.playingTrackActive {
		if m.playlist == nil {
			return
		}
		track, idx := m.playlist.Current()
		if idx < 0 {
			return
		}
		m.playingTrack = track
		m.playingTrackActive = true
	}
	m.playbackDetached = true
}

func (m *Model) clearPlaybackTrack() {
	m.requestedTrack = playlist.Track{}
	m.requestedTrackActive = false
	m.requestedTrackGen = 0
	m.playingTrack = playlist.Track{}
	m.playingTrackActive = false
	m.playingTrackGen = 0
	m.playbackDetached = false
}

// stopPlayback stops audio and clears the active track. It also advances the
// stream generation so a yt-dlp or HTTP stream still spinning up for the
// previous track is refused when it becomes ready, instead of starting to play
// seconds after the user stopped or ran past the end of the queue.
func (m *Model) stopPlayback() {
	nextRequest(&m.requests.stream)
	m.player.SetPlaybackGeneration(m.requests.stream)
	m.player.Stop()
	// The refused stream result would have cleared this; nothing else will.
	m.buffering = false
	m.clearPlaybackTrack()
}
