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

const (
	coverGutterWidth    = 2
	coverMinRows        = 5
	coverMinHeaderWidth = 44
)

type coverArtLoadedMsg struct {
	img image.Image
	err error
	src string
	gen uint64
}

func coverSizeRows(size string) int {
	switch config.NormalizeCoverArtSize(size) {
	case config.CoverArtSmall:
		return 6
	case config.CoverArtLarge:
		return 1 << 30
	default:
		return 9
	}
}

func (m Model) coverArtGeometry(l frameLayout) (cols, rows int) {
	if !m.coverArt.enabled || l.tier != layoutFull || !m.providerIsSpotify() {
		return 0, 0
	}
	if m.fullVis || m.usesContentFirstLayout() || m.usesSimplifiedLayout() || m.visualizerDisabled() {
		return 0, 0
	}
	rows = min(coverSizeRows(m.coverArt.size), 4+l.visualizerRows-1)
	if rows < coverMinRows {
		return 0, 0
	}
	cols = 2 * rows
	if l.panelWidth-cols-coverGutterWidth < coverMinHeaderWidth {
		return 0, 0
	}
	return cols, rows
}

func (m Model) providerIsSpotify() bool {
	if m.provider == nil || m.provPillIdx < 0 || m.provPillIdx >= len(m.providers) {
		return false
	}
	return m.providers[m.provPillIdx].Key == "spotify"
}

func (m Model) coverArtVisible() bool { return m.layout.coverCols > 0 }

func (m Model) headerWidth() int {
	if m.layout.coverCols > 0 {
		return max(1, m.layout.panelWidth-m.layout.coverCols-coverGutterWidth)
	}
	return ui.PanelWidth
}

func (m *Model) toggleCoverArt() tea.Cmd {
	m.coverArt.enabled = !m.coverArt.enabled
	m.saveConfigKey("cover_art", strconv.FormatBool(m.coverArt.enabled))
	m.refreshChrome()
	if !m.coverArt.enabled {
		return nil
	}
	return m.refreshCoverArt()
}

func (m *Model) refreshCoverArt() tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.setCoverImage(nil)
		m.coverArt.err = nil
		m.coverArt.loading = false
		m.coverArt.src = ""
		return nil
	}
	return m.refreshCoverArtFor(track)
}

func (m *Model) refreshCoverArtFor(track playlist.Track) tea.Cmd {
	if !m.coverArt.enabled {
		return nil
	}
	src := track.AlbumArtURL
	if src == "" {
		m.setCoverImage(nil)
		m.coverArt.err = nil
		m.coverArt.loading = false
		m.coverArt.src = ""
		return nil
	}
	if m.coverArt.src == src && (m.coverArt.img != nil || m.coverArt.loading) {
		return nil
	}
	m.coverArt.src = src
	m.setCoverImage(nil)
	m.coverArt.err = nil
	m.coverArt.loading = true
	return fetchCoverArtCmd(src, nextRequest(&m.requests.coverArt))
}

func (m *Model) setCoverImage(img image.Image) {
	m.coverArt.img = img
	m.coverArt.rendered = nil
	m.rerenderCoverArt()
}

func (m *Model) rerenderCoverArt() {
	w, rows := m.layout.coverCols, m.layout.coverRows
	if m.coverArt.rendered != nil && m.coverArt.renderedCols == w && m.coverArt.renderedRows == rows {
		return
	}
	m.coverArt.rendered = nil
	m.coverArt.renderedCols, m.coverArt.renderedRows = w, rows
	if m.coverArt.img == nil || w <= 0 || rows <= 0 {
		return
	}
	if cols, fit := coverart.Fit(m.coverArt.img.Bounds(), w, rows); cols > 0 {
		m.coverArt.rendered = strings.Split(coverart.Render(m.coverArt.img, cols, fit), "\n")
	}
}

func (m Model) renderCoverColumn() string {
	w := m.layout.coverCols
	rows := m.layout.coverRows
	if w <= 0 || rows <= 0 {
		return ""
	}

	var art []string
	switch {
	case m.coverArt.img != nil:
		art = append([]string(nil), m.coverArt.rendered...)
	case m.coverArt.loading:
		art = []string{dimStyle.Render(truncate("Loading art...", w))}
	case m.coverArt.err != nil:
		art = []string{dimStyle.Render(truncate("No art", w))}
	}
	for len(art) < rows {
		art = append(art, "")
	}
	art = art[:rows]
	for i, line := range art {
		art[i] = padCell(line, w)
	}
	return strings.Join(append([]string{padCell(m.playbackStatus(), w)}, art...), "\n")
}

func padCell(text string, width int) string {
	text = ansi.Truncate(text, width, "")
	if gap := width - lipgloss.Width(text); gap > 0 {
		return text + strings.Repeat(" ", gap)
	}
	return text
}

func (m Model) renderHeaderWithCover() string {
	text := []string{
		m.renderTitle(),
		m.renderTrackInfo(),
		m.renderTimeStatus(),
		"",
	}
	if spectrum := m.renderSpectrum(); spectrum != "" {
		text = append(text, spectrum)
	}
	return lipgloss.JoinHorizontal(
		lipgloss.Top,
		strings.Join(text, "\n"),
		strings.Repeat(" ", coverGutterWidth),
		m.renderCoverColumn(),
	)
}
