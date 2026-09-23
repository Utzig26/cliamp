package model

import (
	"image"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/bjarneo/cliamp/config"
	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui"
	"github.com/bjarneo/cliamp/ui/coverart"
)

// Album art geometry. The artwork sits left of the header block — title, track,
// time, a blank, and the visualizer — and the playback status sits under it, so
// the tallest cover the header can hold is one row short of that block.
const (
	// coverGutterWidth separates the artwork from the header text.
	coverGutterWidth = 2
	// coverMinRows is the shortest cover worth drawing; below this the image
	// carries no detail and the row is better spent on the visualizer.
	coverMinRows = 5
	// coverMinHeaderWidth is the width the header text keeps for itself. The
	// artwork is dropped rather than squeezing the track line below this.
	coverMinHeaderWidth = 44
)

// coverArtLoadedMsg carries a decoded cover back to the UI. gen discards the
// results of fetches the user has already moved past.
type coverArtLoadedMsg struct {
	img image.Image
	err error
	src string
	gen uint64
}

// coverSizeRows is the cover height each configured size asks for. The layout
// clamps these to the header it actually has, so "large" fills it.
func coverSizeRows(size string) int {
	switch config.NormalizeCoverArtSize(size) {
	case config.CoverArtSmall:
		return 6
	case config.CoverArtLarge:
		return 1 << 30 // clamped to the header height below
	default:
		return 9
	}
}

// coverArtGeometry returns the cell box the artwork gets for this layout, or
// zeroes when it should not be drawn.
//
// Artwork is a full-tier luxury: it needs the header block that only that tier
// draws, so it disappears one tier before the visualizer does rather than
// fighting the denser layouts for rows.
func (m Model) coverArtGeometry(l frameLayout) (cols, rows int) {
	if !m.coverArt.enabled || l.tier != layoutFull {
		return 0, 0
	}
	if m.usesContentFirstLayout() || m.usesSimplifiedLayout() || m.visualizerDisabled() {
		return 0, 0
	}
	// The header block is title, track, time, a blank, and the visualizer; the
	// status line under the artwork claims one row of it.
	rows = min(coverSizeRows(m.coverArt.size), 4+l.visualizerRows-1)
	if rows < coverMinRows {
		return 0, 0
	}
	// A terminal cell is about twice as tall as it is wide, so a square cover
	// spans twice as many columns as rows.
	cols = 2 * rows
	if l.panelWidth-cols-coverGutterWidth < coverMinHeaderWidth {
		return 0, 0
	}
	return cols, rows
}

// coverArtVisible reports whether this frame draws artwork.
func (m Model) coverArtVisible() bool { return m.layout.coverCols > 0 }

// headerWidth is the width the header text has once the artwork takes its
// share. Without artwork the header owns the whole panel.
func (m Model) headerWidth() int {
	if m.layout.coverCols > 0 {
		return max(1, m.layout.panelWidth-m.layout.coverCols-coverGutterWidth)
	}
	return ui.PanelWidth
}

// toggleCoverArt turns the artwork on or off and remembers the choice.
func (m *Model) toggleCoverArt() tea.Cmd {
	m.coverArt.enabled = !m.coverArt.enabled
	m.saveConfigKey("cover_art", strconv.FormatBool(m.coverArt.enabled))
	m.refreshChrome()
	if !m.coverArt.enabled {
		return nil
	}
	return m.refreshCoverArt()
}

// refreshCoverArt fetches the playing track's artwork.
func (m *Model) refreshCoverArt() tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.coverArt.img = nil
		m.coverArt.err = nil
		m.coverArt.loading = false
		m.coverArt.src = ""
		return nil
	}
	return m.refreshCoverArtFor(track)
}

// refreshCoverArtFor fetches track's artwork, skipping the work when that image
// is already held or in flight. Callers may invoke it on every track change; it
// settles to a no-op while one track plays.
func (m *Model) refreshCoverArtFor(track playlist.Track) tea.Cmd {
	if !m.coverArt.enabled {
		return nil
	}
	src := track.AlbumArtURL
	if src == "" {
		m.coverArt.img = nil
		m.coverArt.err = nil
		m.coverArt.loading = false
		m.coverArt.src = ""
		return nil
	}
	if m.coverArt.src == src && (m.coverArt.img != nil || m.coverArt.loading) {
		return nil
	}
	m.coverArt.src = src
	m.coverArt.img = nil
	m.coverArt.err = nil
	m.coverArt.loading = true
	return fetchCoverArtCmd(src, nextRequest(&m.requests.coverArt))
}

// renderCoverColumn draws the artwork with the playback status beneath it, as
// one block exactly coverCols wide so the header text keeps its own column.
func (m Model) renderCoverColumn() string {
	w := m.layout.coverCols
	rows := m.layout.coverRows
	if w <= 0 || rows <= 0 {
		return ""
	}

	var art []string
	switch {
	case m.coverArt.img != nil:
		cols, fit := coverart.Fit(m.coverArt.img.Bounds(), w, rows)
		if cols > 0 {
			art = strings.Split(coverart.Render(m.coverArt.img, cols, fit), "\n")
		}
	case m.coverArt.loading:
		art = []string{dimStyle.Render(truncate("Loading art...", w))}
	case m.coverArt.err != nil:
		art = []string{dimStyle.Render(truncate("No art", w))}
	}
	// Pad to the full box so the status always lands on the last row and the
	// header beside it never shifts as the image loads.
	for len(art) < rows {
		art = append(art, "")
	}
	art = art[:rows]
	for i, line := range art {
		art[i] = padCell(line, w)
	}
	return strings.Join(append(art, padCell(m.playbackStatus(), w)), "\n")
}

// padCell trims text to width and pads it out, so a column of them is a solid
// rectangle that lipgloss can set beside another without either shifting.
func padCell(text string, width int) string {
	text = ansi.Truncate(text, width, "")
	if gap := width - lipgloss.Width(text); gap > 0 {
		return text + strings.Repeat(" ", gap)
	}
	return text
}

// renderHeaderWithCover joins the artwork column and the header text. Both
// columns are padded to a fixed height, so lipgloss aligns them from the top
// without either stretching the other.
func (m Model) renderHeaderWithCover() string {
	right := []string{
		m.renderTitle(),
		m.renderTrackInfo(),
		m.renderTimeStatus(),
		"",
	}
	if spectrum := m.renderSpectrum(); spectrum != "" {
		right = append(right, spectrum)
	}
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.renderCoverColumn(),
		strings.Repeat(" ", coverGutterWidth),
		strings.Join(right, "\n"),
	)
}
