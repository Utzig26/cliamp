package model

import (
	"image"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/bjarneo/cliamp/playlist"
	"github.com/bjarneo/cliamp/ui/coverart"
)

// coverArtLoadedMsg carries a decoded cover back to the UI. gen discards the
// results of fetches the user has already moved past.
type coverArtLoadedMsg struct {
	img image.Image
	err error
	src string
	gen uint64
}

// toggleCoverArt opens or closes the artwork overlay, fetching on open.
func (m *Model) toggleCoverArt() tea.Cmd {
	m.coverArt.visible = !m.coverArt.visible
	if !m.coverArt.visible {
		return nil
	}
	return m.refreshCoverArt()
}

// refreshCoverArt fetches the playing track's artwork.
func (m *Model) refreshCoverArt() tea.Cmd {
	track, idx := m.currentPlaybackTrack()
	if idx < 0 {
		m.coverArt = coverArtState{visible: m.coverArt.visible}
		return nil
	}
	return m.refreshCoverArtFor(track)
}

// refreshCoverArtFor fetches track's artwork, skipping the work when that image
// is already held or in flight. Callers may invoke it on every track change; it
// settles to a no-op while one track plays.
func (m *Model) refreshCoverArtFor(track playlist.Track) tea.Cmd {
	src := track.AlbumArtURL
	if src == "" {
		m.coverArt = coverArtState{visible: m.coverArt.visible}
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

// retryCoverArt refetches after a failure, bypassing the already-held check.
func (m *Model) retryCoverArt() tea.Cmd {
	if m.coverArt.loading {
		return nil
	}
	m.coverArt.src = ""
	return m.refreshCoverArt()
}

func (m Model) coverArtHelpLine() string {
	return m.commandHelp(commandModeCoverArt)
}

// renderCoverArtBody draws the artwork centred in the body region. The image is
// re-rendered from the decoded original on every call, so a resize refits it
// without another fetch.
func (m Model) renderCoverArtBody() string {
	rows := m.effectivePlaylistVisible()
	if rows <= 0 {
		return ""
	}
	switch {
	case m.coverArt.loading:
		return dimStyle.Render("  Loading cover art...")
	case m.coverArt.err != nil:
		return errorStyle.Render("  Cover art failed: " + m.coverArt.err.Error())
	case m.coverArt.img == nil:
		return dimStyle.Render("  No cover art for this track.")
	}

	width := m.layout.panelWidth
	cols, artRows := coverart.Fit(m.coverArt.img.Bounds(), width, rows)
	if cols == 0 {
		return dimStyle.Render("  Not enough room to draw the cover.")
	}

	lines := strings.Split(coverart.Render(m.coverArt.img, cols, artRows), "\n")
	pad := strings.Repeat(" ", max(0, (width-cols)/2))
	for i, line := range lines {
		lines[i] = pad + line
	}
	return strings.Join(lines, "\n")
}
