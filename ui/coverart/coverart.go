package coverart

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bjarneo/cliamp/internal/httpclient"
)

const maxBytes = 12 << 20

const maxPixels = 4096 * 4096

const upperHalf = '▀'

var blockedIP = func(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

var client = func() *http.Client {
	c := *httpclient.Streaming
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("cover art: too many redirects")
		}
		return checkDestination(req.Context(), req.URL)
	}
	return &c
}()

func checkDestination(ctx context.Context, u *url.URL) error {
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("cover art: unsupported scheme %q", u.Scheme)
	}
	host := u.Hostname()
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		var err error
		if ips, err = net.DefaultResolver.LookupIP(ctx, "ip", host); err != nil {
			return fmt.Errorf("cover art: resolve %s: %w", host, err)
		}
	}
	for _, ip := range ips {
		if blockedIP(ip) {
			return fmt.Errorf("cover art: %s is not a public address", host)
		}
	}
	return nil
}

func decodeBounded(r io.Reader) (image.Image, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || uint64(cfg.Width)*uint64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("image too large: %dx%d", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

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
		img, err := decodeBounded(f)
		if err != nil {
			return nil, fmt.Errorf("cover art: decode %s: %w", path, err)
		}
		return img, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("cover art: request: %w", err)
	}
	if err := checkDestination(ctx, req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httpclient.UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover art: fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art: fetch: http status %s", resp.Status)
	}
	img, err := decodeBounded(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("cover art: decode: %w", err)
	}
	return img, nil
}

func Fit(b image.Rectangle, maxCols, maxRows int) (cols, rows int) {
	w, h := b.Dx(), b.Dy()
	if maxCols < 2 || maxRows < 1 || w <= 0 || h <= 0 {
		return 0, 0
	}
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

func Render(img image.Image, cols, rows int) string {
	if img == nil || cols < 1 || rows < 1 {
		return ""
	}
	px := sample(img, cols, 2*rows)

	var b strings.Builder
	b.Grow(rows * cols * 40)
	for y := range rows {
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
