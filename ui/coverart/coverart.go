// Package coverart renders album artwork as terminal text.
//
// Artwork is drawn with the upper-half-block character: every cell carries two
// stacked pixels, the top one as the foreground colour and the bottom one as
// the background. The output stays ordinary styled text, so it measures and
// composes like any other string in the Bubbletea view — unlike terminal
// graphics protocols, whose escape sequences the cell renderer strips.
package coverart

import (
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decode Spotify and Navidrome artwork
	_ "image/png"  // decode embedded local-file artwork
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

// maxBytes caps how much of a response we read. Provider artwork is well under
// this; the limit keeps a misbehaving host from exhausting memory.
const maxBytes = 12 << 20

// upperHalf fills the top half of a cell, leaving the bottom to the background.
const upperHalf = '▀'

// Load decodes the artwork at src, which may be an http(s) URL or a file://
// URL (what the local provider writes for embedded art).
func Load(ctx context.Context, src string) (image.Image, error) {
	if src == "" {
		return nil, fmt.Errorf("cover art: no source")
	}
	if path, ok := strings.CutPrefix(src, "file://"); ok {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("cover art: open %s: %w", path, err)
		}
		defer f.Close()
		img, _, err := image.Decode(io.LimitReader(f, maxBytes))
		if err != nil {
			return nil, fmt.Errorf("cover art: decode %s: %w", path, err)
		}
		return img, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("cover art: request: %w", err)
	}
	req.Header.Set("User-Agent", httpclient.UserAgent)
	resp, err := httpclient.Streaming.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover art: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art: fetch: http status %s", resp.Status)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("cover art: decode: %w", err)
	}
	return img, nil
}

// Fit returns the largest cell box holding b's aspect ratio within maxCols by
// maxRows. A terminal cell is assumed twice as tall as it is wide, and a
// half-block cell stacks two pixels, so a square image yields cols == 2*rows.
func Fit(b image.Rectangle, maxCols, maxRows int) (cols, rows int) {
	w, h := b.Dx(), b.Dy()
	if maxCols < 2 || maxRows < 1 || w <= 0 || h <= 0 {
		return 0, 0
	}
	// Start from the tallest box the width allows, then clamp to the height.
	rows = maxCols * h / (2 * w)
	if rows > maxRows {
		rows = maxRows
	}
	cols = 2 * rows * w / h
	if cols > maxCols {
		cols = maxCols
	}
	if cols < 2 || rows < 1 {
		return 0, 0
	}
	return cols, rows
}

// Render draws img as cols by rows cells of half-block text. Each line resets
// colour at its end, so the caller can pad, centre, or frame the result like
// any other string.
func Render(img image.Image, cols, rows int) string {
	if img == nil || cols < 1 || rows < 1 {
		return ""
	}
	px := sample(img, cols, 2*rows)

	var b strings.Builder
	// Two SGR sequences plus the block run about 40 bytes per cell.
	b.Grow(rows * cols * 40)
	for y := range rows {
		// Track the colours last written so runs of one shade emit the escape
		// once rather than per cell.
		var lastTop, lastBottom color.RGBA
		var started bool
		for x := range cols {
			top, bottom := px[2*y][x], px[2*y+1][x]
			if !started || top != lastTop {
				fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", top.R, top.G, top.B)
				lastTop = top
			}
			if !started || bottom != lastBottom {
				fmt.Fprintf(&b, "\x1b[48;2;%d;%d;%dm", bottom.R, bottom.G, bottom.B)
				lastBottom = bottom
			}
			started = true
			b.WriteRune(upperHalf)
		}
		b.WriteString("\x1b[0m")
		if y < rows-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// sample box-filters img down to w by h pixels. Averaging each source box keeps
// detail that nearest-neighbour drops at the sizes a terminal offers.
func sample(img image.Image, w, h int) [][]color.RGBA {
	b := img.Bounds()
	out := make([][]color.RGBA, h)
	for y := range h {
		row := make([]color.RGBA, w)
		y0 := b.Min.Y + y*b.Dy()/h
		y1 := b.Min.Y + (y+1)*b.Dy()/h
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := range w {
			x0 := b.Min.X + x*b.Dx()/w
			x1 := b.Min.X + (x+1)*b.Dx()/w
			if x1 <= x0 {
				x1 = x0 + 1
			}
			row[x] = average(img, x0, y0, x1, y1)
		}
		out[y] = row
	}
	return out
}

// average returns the mean colour of the source box. RGBA() is
// alpha-premultiplied, so transparent artwork averages towards black rather
// than rendering as stray bright pixels.
func average(img image.Image, x0, y0, x1, y1 int) color.RGBA {
	var sr, sg, sb, n uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			sr += uint64(r)
			sg += uint64(g)
			sb += uint64(bl)
			n++
		}
	}
	if n == 0 {
		return color.RGBA{A: 0xff}
	}
	return color.RGBA{
		R: uint8(sr / n >> 8),
		G: uint8(sg / n >> 8),
		B: uint8(sb / n >> 8),
		A: 0xff,
	}
}
