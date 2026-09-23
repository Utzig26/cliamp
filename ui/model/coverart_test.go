package model

import (
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
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

// coverModel builds a full-tier model with artwork switched on.
func coverModel(t *testing.T, width, height int, size string) Model {
	t.Helper()
	m := newLayoutTestModel(width, height)
	m.coverArt.enabled = true
	m.coverArt.size = config.NormalizeCoverArtSize(size)
	m.coverArt.img = testCover(300, 300)
	m.recomputeLayout()
	return m
}

func TestCoverArtGeometrySizes(t *testing.T) {
	// The header block is title, track, time, a blank, and the visualizer; the
	// status line under the artwork takes one row of it, so a default
	// seven-row visualizer leaves ten.
	tests := []struct {
		name     string
		size     string
		wantRows int
		wantCols int
	}{
		{"small", config.CoverArtSmall, 6, 12},
		{"medium", config.CoverArtMedium, 9, 18},
		{"large fills the header", config.CoverArtLarge, 10, 20},
		{"unknown spelling falls back to medium", "enormous", 9, 18},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := coverModel(t, 120, 40, tt.size)
			if m.layout.coverRows != tt.wantRows || m.layout.coverCols != tt.wantCols {
				t.Errorf("cover box = %dx%d cells, want %dx%d",
					m.layout.coverCols, m.layout.coverRows, tt.wantCols, tt.wantRows)
			}
			// A square cover spans twice as many columns as rows.
			if m.layout.coverCols != 2*m.layout.coverRows {
				t.Errorf("cover is not square on screen: %d cols for %d rows",
					m.layout.coverCols, m.layout.coverRows)
			}
		})
	}
}

func TestCoverArtFitsInsideHeader(t *testing.T) {
	// Whatever the size asks for, the artwork plus its status row must never
	// outgrow the header block it shares with the visualizer.
	for _, size := range []string{config.CoverArtSmall, config.CoverArtMedium, config.CoverArtLarge} {
		for _, dim := range [][2]int{{80, 24}, {100, 30}, {160, 50}, {200, 60}} {
			m := coverModel(t, dim[0], dim[1], size)
			if m.layout.coverRows == 0 {
				continue
			}
			headerRows := 4 + m.layout.visualizerRows
			if m.layout.coverRows+1 > headerRows {
				t.Errorf("%s at %dx%d: cover %d rows + status exceeds header %d",
					size, dim[0], dim[1], m.layout.coverRows, headerRows)
			}
			if left := m.layout.panelWidth - m.layout.coverCols - coverGutterWidth; left < coverMinHeaderWidth {
				t.Errorf("%s at %dx%d: header text left with %d cols, min %d",
					size, dim[0], dim[1], left, coverMinHeaderWidth)
			}
		}
	}
}

func TestCoverArtHiddenWhenItCannotFit(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *Model)
	}{
		{"switched off", func(m *Model) { m.coverArt.enabled = false }},
		{"visualizer off", func(m *Model) { m.vis.Mode = ui.VisNone }},
		{"provider focus takes the frame", func(m *Model) { m.focus = focusProvider }},
		{"simplified view", func(m *Model) { m.simplified = true }},
		{"visualizer too short to share", func(m *Model) { m.visRows = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := coverModel(t, 120, 40, config.CoverArtLarge)
			if m.layout.coverCols == 0 {
				t.Fatal("precondition: artwork should be drawn before the change")
			}
			tt.setup(&m)
			m.recomputeLayout()
			if m.layout.coverCols != 0 || m.layout.coverRows != 0 {
				t.Errorf("artwork still drawn at %dx%d cells", m.layout.coverCols, m.layout.coverRows)
			}
		})
	}
}

func TestCoverArtLeavesBeforeTheVisualizer(t *testing.T) {
	// The artwork needs the full tier's header, so it goes one tier earlier
	// than the visualizer, which the compact tier still draws.
	compact := newLayoutTestModel(70, 20)
	compact.coverArt.enabled = true
	compact.coverArt.size = config.CoverArtLarge
	compact.recomputeLayout()

	if compact.layout.tier != layoutCompact {
		t.Fatalf("precondition: tier = %v, want compact", compact.layout.tier)
	}
	if compact.layout.visualizerRows == 0 {
		t.Fatal("precondition: the compact tier should still draw a visualizer")
	}
	if compact.layout.coverCols != 0 {
		t.Error("artwork should be gone while the visualizer is still drawn")
	}
}

func TestCoverArtDoesNotStealBodyRows(t *testing.T) {
	// Artwork claims width beside the header, never rows from the playlist.
	off := newLayoutTestModel(120, 40)
	on := coverModel(t, 120, 40, config.CoverArtLarge)

	if on.layout.coverCols == 0 {
		t.Fatal("precondition: artwork should be drawn")
	}
	if on.layout.bodyRows != off.layout.bodyRows {
		t.Errorf("bodyRows = %d with artwork, %d without", on.layout.bodyRows, off.layout.bodyRows)
	}
	if on.layout.fixedRows != off.layout.fixedRows {
		t.Errorf("fixedRows = %d with artwork, %d without", on.layout.fixedRows, off.layout.fixedRows)
	}
}

func TestCoverArtNarrowsTheSpectrum(t *testing.T) {
	off := newLayoutTestModel(120, 40)
	on := coverModel(t, 120, 40, config.CoverArtMedium)

	want := on.layout.panelWidth - on.layout.coverCols - coverGutterWidth
	if on.vis.Cols != want {
		t.Errorf("visualizer width = %d, want %d", on.vis.Cols, want)
	}
	if on.vis.Cols >= off.vis.Cols {
		t.Errorf("visualizer did not narrow: %d with artwork, %d without", on.vis.Cols, off.vis.Cols)
	}
	if on.vis.Rows != off.vis.Rows {
		t.Errorf("visualizer height changed: %d with artwork, %d without", on.vis.Rows, off.vis.Rows)
	}
}

func TestHeaderWidth(t *testing.T) {
	off := newLayoutTestModel(120, 40)
	if got, want := off.headerWidth(), ui.PanelWidth; got != want {
		t.Errorf("headerWidth() without artwork = %d, want the panel width %d", got, want)
	}
	on := coverModel(t, 120, 40, config.CoverArtMedium)
	want := on.layout.panelWidth - on.layout.coverCols - coverGutterWidth
	if got := on.headerWidth(); got != want {
		t.Errorf("headerWidth() with artwork = %d, want %d", got, want)
	}
}

func TestRenderCoverColumnDimensions(t *testing.T) {
	states := map[string]coverArtState{
		"drawn":   {img: testCover(300, 300)},
		"loading": {loading: true},
		"failed":  {err: errors.New("no route to host")},
		"no art":  {},
	}
	for name, st := range states {
		t.Run(name, func(t *testing.T) {
			m := coverModel(t, 120, 40, config.CoverArtMedium)
			m.coverArt.img, m.coverArt.loading, m.coverArt.err = st.img, st.loading, st.err

			lines := strings.Split(m.renderCoverColumn(), "\n")
			// Every state pads to the same box so the header beside it never
			// shifts as the image loads.
			if len(lines) != m.layout.coverRows+1 {
				t.Fatalf("column has %d lines, want %d", len(lines), m.layout.coverRows+1)
			}
			for i, line := range lines {
				if w := ansi.StringWidth(line); w != m.layout.coverCols {
					t.Errorf("line %d width = %d, want %d", i, w, m.layout.coverCols)
				}
			}
		})
	}
}

func TestRenderHeaderWithCoverFitsPanel(t *testing.T) {
	for _, size := range []string{config.CoverArtSmall, config.CoverArtMedium, config.CoverArtLarge} {
		for _, dim := range [][2]int{{80, 24}, {120, 40}, {200, 60}} {
			m := coverModel(t, dim[0], dim[1], size)
			if m.layout.coverCols == 0 {
				continue
			}
			for i, line := range strings.Split(m.renderHeaderWithCover(), "\n") {
				if w := ansi.StringWidth(line); w > m.layout.panelWidth {
					t.Errorf("%s at %dx%d: header line %d is %d wide, panel is %d",
						size, dim[0], dim[1], i, w, m.layout.panelWidth)
				}
			}
		}
	}
}

func TestTimeStatusMovesUnderTheCover(t *testing.T) {
	off := newLayoutTestModel(120, 40)
	if !strings.Contains(off.renderTimeStatus(), "Stopped") {
		t.Error("without artwork the status should share the time row")
	}
	on := coverModel(t, 120, 40, config.CoverArtMedium)
	if strings.Contains(on.renderTimeStatus(), "Stopped") {
		t.Error("with artwork the status should leave the time row")
	}
	if !strings.Contains(on.renderCoverColumn(), "Stopped") {
		t.Error("the status should sit under the artwork")
	}
}

func TestRefreshCoverArtFor(t *testing.T) {
	tests := []struct {
		name        string
		enabled     bool
		before      coverArtState
		track       playlist.Track
		wantCmd     bool
		wantLoading bool
		wantSrc     string
	}{
		{
			name:    "switched off does nothing",
			enabled: false,
			track:   playlist.Track{AlbumArtURL: "https://art/one.jpg"},
			wantCmd: false,
		},
		{
			name:    "track without art clears state",
			enabled: true,
			before:  coverArtState{src: "old", img: testCover(4, 4)},
			track:   playlist.Track{Title: "No art"},
			wantCmd: false,
		},
		{
			name:        "new art starts a fetch",
			enabled:     true,
			track:       playlist.Track{AlbumArtURL: "https://art/one.jpg"},
			wantCmd:     true,
			wantLoading: true,
			wantSrc:     "https://art/one.jpg",
		},
		{
			name:        "different art refetches",
			enabled:     true,
			before:      coverArtState{src: "https://art/one.jpg", img: testCover(4, 4)},
			track:       playlist.Track{AlbumArtURL: "https://art/two.jpg"},
			wantCmd:     true,
			wantLoading: true,
			wantSrc:     "https://art/two.jpg",
		},
		{
			name:    "same art already held is a no-op",
			enabled: true,
			before:  coverArtState{src: "https://art/one.jpg", img: testCover(4, 4)},
			track:   playlist.Track{AlbumArtURL: "https://art/one.jpg"},
			wantCmd: false,
			wantSrc: "https://art/one.jpg",
		},
		{
			name:        "same art already in flight is a no-op",
			enabled:     true,
			before:      coverArtState{src: "https://art/one.jpg", loading: true},
			track:       playlist.Track{AlbumArtURL: "https://art/one.jpg"},
			wantCmd:     false,
			wantLoading: true,
			wantSrc:     "https://art/one.jpg",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := tt.before
			st.enabled = tt.enabled
			m := Model{coverArt: st}

			cmd := m.refreshCoverArtFor(tt.track)

			if gotCmd := cmd != nil; gotCmd != tt.wantCmd {
				t.Errorf("returned a command = %v, want %v", gotCmd, tt.wantCmd)
			}
			if !tt.enabled {
				return
			}
			if m.coverArt.loading != tt.wantLoading {
				t.Errorf("loading = %v, want %v", m.coverArt.loading, tt.wantLoading)
			}
			if m.coverArt.src != tt.wantSrc {
				t.Errorf("src = %q, want %q", m.coverArt.src, tt.wantSrc)
			}
		})
	}
}

func TestToggleCoverArt(t *testing.T) {
	m := newLayoutTestModel(120, 40)
	m.playlist.Replace([]playlist.Track{{Title: "Song", AlbumArtURL: "https://art/one.jpg"}})
	m.playlist.SetIndex(0)

	if cmd := m.toggleCoverArt(); cmd == nil {
		t.Fatal("switching artwork on should fetch it")
	}
	if !m.coverArt.enabled {
		t.Fatal("artwork should be enabled after the first toggle")
	}
	if cmd := m.toggleCoverArt(); cmd != nil {
		t.Error("switching artwork off should not fetch")
	}
	if m.coverArt.enabled {
		t.Error("artwork should be disabled after the second toggle")
	}
}
