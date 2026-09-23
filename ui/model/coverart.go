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
	if !m.coverArt.enabled || l.tier != layoutFull {
		return 0, 0
	}
	if m.usesContentFirstLayout() || m.usesSimplifiedLayout() || m.visualizerDisabled() {
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
		m.coverArt.img = nil
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
	for len(art) < rows {
		art = append(art, "")
	}
	art = art[:rows]
	for i, line := range art {
		art[i] = padCell(line, w)
	}
	return strings.Join(append(art, padCell(m.playbackStatus(), w)), "\n")
}

func padCell(text string, width int) string {
	text = ansi.Truncate(text, width, "")
	if gap := width - lipgloss.Width(text); gap > 0 {
		return text + strings.Repeat(" ", gap)
	}
	return text
}

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
