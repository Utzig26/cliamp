package model

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bjarneo/cliamp/internal/plugintrust"
	"github.com/bjarneo/cliamp/luaplugin"
	"github.com/bjarneo/cliamp/playlist"
)

// nowPlayingProv records ReportNowPlaying calls, which nowPlaying issues
// alongside the plugin track.change event.
type nowPlayingProv struct {
	plainProv
	reports chan playlist.Track
}

func TestPlayTrackEmitsPluginTrackChange(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLIAMP_CONFIG_DIR", configDir)
	pluginDir := filepath.Join(configDir, "plugins")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pluginPath := filepath.Join(pluginDir, "track-change.lua")
	const script = `
local p = plugin.register({name = "track-change", type = "hook"})
p:on("track.change", function(track)
    cliamp.message(track.path .. "\n" .. track.artist .. "\n" .. track.title)
end)
`
	if err := os.WriteFile(pluginPath, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugintrust.Approve(pluginDir, "track-change", pluginPath); err != nil {
		t.Fatal(err)
	}
	mgr, err := luaplugin.New(nil, nil)
	t.Cleanup(mgr.Close)
	if err != nil {
		t.Fatal(err)
	}
	if !mgr.HasHook(luaplugin.EventTrackChange) {
		t.Fatal("test plugin did not register track.change")
	}
	messages := make(chan string, 1)
	ctx := t.Context()
	mgr.SetUIProvider(luaplugin.UIProvider{
		ShowMessage: func(text string, _ time.Duration) {
			select {
			case messages <- text:
			case <-ctx.Done():
			}
		},
	})

	for _, tc := range []struct {
		name     string
		path     string
		reporter bool
	}{
		{"youtube without reporter", "https://www.youtube.com/watch?v=GBRAnuT48qo", false},
		{"youtube with reporter", "https://www.youtube.com/watch?v=GBRAnuT48qo", true},
		{"soundcloud without reporter", "https://soundcloud.com/artist/track", false},
		{"soundcloud with reporter", "https://soundcloud.com/artist/track", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			track := playlist.Track{Path: tc.path, Title: tc.name, Artist: "Artist", Stream: true}
			pl := playlist.New()
			pl.Add(track)
			m := Model{player: &playbackFakeEngine{}, playlist: pl, luaMgr: mgr}
			if tc.reporter {
				prov := &nowPlayingProv{reports: make(chan playlist.Track, 1)}
				m.providers = []ProviderEntry{{Key: "p", Name: "P", Provider: prov}}
			}
			m.playTrack(track)
			select {
			case got := <-messages:
				if want := track.Path + "\n" + track.Artist + "\n" + track.Title; got != want {
					t.Fatalf("plugin received %q, want %q", got, want)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("plugin did not receive track.change")
			}
		})
	}
}

func (p *nowPlayingProv) CanReportPlayback(playlist.Track) bool { return true }

func (p *nowPlayingProv) ReportNowPlaying(t playlist.Track, _ time.Duration, _ bool) error {
	p.reports <- t
	return nil
}

func (p *nowPlayingProv) ReportScrobble(playlist.Track, time.Duration, time.Duration, bool) error {
	return nil
}

// TestPlayTrackFiresNowPlayingForEverySource guards against the yt-dlp branch
// of playTrack returning before nowPlaying, which silently dropped the
// track.change plugin event for YouTube and SoundCloud tracks.
func TestPlayTrackFiresNowPlayingForEverySource(t *testing.T) {
	for _, path := range []string{
		"/music/local.flac",
		"https://example.com/stream.mp3",
		"https://www.youtube.com/watch?v=GBRAnuT48qo",
		"https://music.youtube.com/watch?v=GBRAnuT48qo",
		"https://soundcloud.com/artist/track",
	} {
		t.Run(path, func(t *testing.T) {
			prov := &nowPlayingProv{reports: make(chan playlist.Track, 1)}
			pl := playlist.New()
			track := playlist.Track{Path: path, Title: "T", Stream: playlist.IsURL(path)}
			pl.Add(track)
			m := Model{
				player:    &playbackFakeEngine{},
				playlist:  pl,
				providers: []ProviderEntry{{Key: "p", Name: "P", Provider: prov}},
			}
			m.playTrack(track)
			select {
			case got := <-prov.reports:
				if got.Path != path {
					t.Fatalf("now-playing reported %q, want %q", got.Path, path)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("playTrack did not fire nowPlaying")
			}
		})
	}
}
