package model

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/playlist"
)

func testCover(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 0xff})
		}
	}
	return img
}

func TestRefreshCoverArtFor(t *testing.T) {
	tests := []struct {
		name        string
		before      coverArtState
		track       playlist.Track
		wantCmd     bool
		wantLoading bool
		wantSrc     string
	}{
		{
			name:    "track without art clears state",
			before:  coverArtState{visible: true, src: "old", img: testCover(4, 4)},
			track:   playlist.Track{Title: "No art"},
			wantCmd: false,
			wantSrc: "",
		},
		{
			name:        "new art starts a fetch",
			before:      coverArtState{visible: true},
			track:       playlist.Track{Title: "Song", AlbumArtURL: "https://art/one.jpg"},
			wantCmd:     true,
			wantLoading: true,
			wantSrc:     "https://art/one.jpg",
		},
		{
			name:        "different art refetches",
			before:      coverArtState{visible: true, src: "https://art/one.jpg", img: testCover(4, 4)},
			track:       playlist.Track{Title: "Song", AlbumArtURL: "https://art/two.jpg"},
			wantCmd:     true,
			wantLoading: true,
			wantSrc:     "https://art/two.jpg",
		},
		{
			name:    "same art already held is a no-op",
			before:  coverArtState{visible: true, src: "https://art/one.jpg", img: testCover(4, 4)},
			track:   playlist.Track{Title: "Song", AlbumArtURL: "https://art/one.jpg"},
			wantCmd: false,
			wantSrc: "https://art/one.jpg",
		},
		{
			name:        "same art already in flight is a no-op",
			before:      coverArtState{visible: true, src: "https://art/one.jpg", loading: true},
			track:       playlist.Track{Title: "Song", AlbumArtURL: "https://art/one.jpg"},
			wantCmd:     false,
			wantLoading: true,
			wantSrc:     "https://art/one.jpg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{coverArt: tt.before}
			cmd := m.refreshCoverArtFor(tt.track)

			if gotCmd := cmd != nil; gotCmd != tt.wantCmd {
				t.Errorf("returned a command = %v, want %v", gotCmd, tt.wantCmd)
			}
			if m.coverArt.loading != tt.wantLoading {
				t.Errorf("loading = %v, want %v", m.coverArt.loading, tt.wantLoading)
			}
			if m.coverArt.src != tt.wantSrc {
				t.Errorf("src = %q, want %q", m.coverArt.src, tt.wantSrc)
			}
			if !m.coverArt.visible {
				t.Error("refresh must not close the overlay")
			}
		})
	}
}

func TestRefreshCoverArtForStartsCleanFetch(t *testing.T) {
	m := Model{coverArt: coverArtState{visible: true, img: testCover(4, 4), err: errors.New("stale")}}
	m.refreshCoverArtFor(playlist.Track{AlbumArtURL: "https://art/new.jpg"})
	if m.coverArt.img != nil {
		t.Error("the previous image should be dropped while the new one loads")
	}
	if m.coverArt.err != nil {
		t.Error("the previous error should be cleared while the new one loads")
	}
}

func TestRetryCoverArtIgnoresHeldImage(t *testing.T) {
	p := playlist.New()
	p.Replace([]playlist.Track{{Title: "Song", AlbumArtURL: "https://art/one.jpg"}})
	p.SetIndex(0)

	m := Model{playlist: p, coverArt: coverArtState{visible: true, src: "https://art/one.jpg", err: errors.New("boom")}}
	if cmd := m.retryCoverArt(); cmd == nil {
		t.Fatal("retry should refetch the same source")
	}
	if !m.coverArt.loading {
		t.Error("retry should mark the fetch in flight")
	}

	m.coverArt.loading = true
	if cmd := m.retryCoverArt(); cmd != nil {
		t.Error("retry should not stack a second in-flight fetch")
	}
}

func TestToggleCoverArt(t *testing.T) {
	p := playlist.New()
	p.Replace([]playlist.Track{{Title: "Song", AlbumArtURL: "https://art/one.jpg"}})
	p.SetIndex(0)
	m := Model{playlist: p}

	if cmd := m.toggleCoverArt(); cmd == nil {
		t.Fatal("opening the overlay should start a fetch")
	}
	if !m.coverArt.visible {
		t.Fatal("overlay should be visible after the first toggle")
	}
	if cmd := m.toggleCoverArt(); cmd != nil {
		t.Error("closing the overlay should not fetch")
	}
	if m.coverArt.visible {
		t.Error("overlay should be hidden after the second toggle")
	}
}

func TestRenderCoverArtBodyStates(t *testing.T) {
	tests := []struct {
		name     string
		state    coverArtState
		rows     int
		contains string
		art      bool
	}{
		{"no room", coverArtState{img: testCover(64, 64)}, 0, "", false},
		{"loading", coverArtState{loading: true}, 10, "Loading cover art", false},
		{"failed", coverArtState{err: errors.New("no route to host")}, 10, "no route to host", false},
		{"no art", coverArtState{}, 10, "No cover art", false},
		{"drawn", coverArtState{img: testCover(64, 64)}, 10, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Model{coverArt: tt.state, plVisible: tt.rows}
			m.layout.panelWidth = 60

			got := m.renderCoverArtBody()
			if tt.contains != "" && !strings.Contains(got, tt.contains) {
				t.Fatalf("renderCoverArtBody() = %q, want it to mention %q", got, tt.contains)
			}
			if !tt.art {
				return
			}
			lines := strings.Split(got, "\n")
			if len(lines) > tt.rows {
				t.Errorf("drew %d lines into %d rows", len(lines), tt.rows)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w > m.layout.panelWidth {
					t.Errorf("line %d width = %d, exceeds panel width %d", i, w, m.layout.panelWidth)
				}
			}
		})
	}
}

func TestRenderCoverArtBodyFitsResize(t *testing.T) {
	// The decoded image is kept, so every width must redraw from it without a
	// refetch and still respect the panel.
	m := Model{coverArt: coverArtState{img: testCover(300, 300)}, plVisible: 12}
	for _, width := range []int{20, 40, 80, 120} {
		m.layout.panelWidth = width
		for i, line := range strings.Split(m.renderCoverArtBody(), "\n") {
			if w := ansi.StringWidth(line); w > width {
				t.Errorf("width %d: line %d measured %d", width, i, w)
			}
		}
	}
}

func TestCoverArtScreenLabel(t *testing.T) {
	if got := screenCoverArt.label(); got != "Cover Art" {
		t.Errorf("screenCoverArt.label() = %q, want %q", got, "Cover Art")
	}
	m := Model{coverArt: coverArtState{visible: true}}
	if got := m.activeScreen(); got != screenCoverArt {
		t.Errorf("activeScreen() = %v, want screenCoverArt", got)
	}
}
