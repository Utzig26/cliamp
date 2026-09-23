package coverart

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestFit(t *testing.T) {
	tests := []struct {
		name             string
		w, h             int
		maxCols, maxRows int
		wantCols         int
		wantRows         int
	}{
		{"square fills height", 600, 600, 80, 20, 40, 20},
		{"square limited by width", 600, 600, 30, 40, 30, 15},
		{"wide banner", 1200, 300, 80, 40, 80, 10},
		{"tall poster", 300, 600, 80, 20, 20, 20},
		{"box too narrow", 600, 600, 1, 20, 0, 0},
		{"box too short", 600, 600, 80, 0, 0, 0},
		{"degenerate image", 0, 0, 80, 20, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cols, rows := Fit(image.Rect(0, 0, tt.w, tt.h), tt.maxCols, tt.maxRows)
			if cols != tt.wantCols || rows != tt.wantRows {
				t.Errorf("Fit() = %d,%d want %d,%d", cols, rows, tt.wantCols, tt.wantRows)
			}
			if cols > tt.maxCols || rows > tt.maxRows {
				t.Errorf("Fit() = %d,%d exceeds box %d,%d", cols, rows, tt.maxCols, tt.maxRows)
			}
		})
	}
}

// solid builds a w by h image of one colour.
func solid(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestRenderDimensions(t *testing.T) {
	tests := []struct {
		name       string
		cols, rows int
		wantLines  int
	}{
		{"single cell", 1, 1, 1},
		{"square block", 8, 4, 4},
		{"wide strip", 20, 1, 1},
		{"zero cols", 0, 4, 0},
		{"zero rows", 8, 0, 0},
	}
	img := solid(16, 16, color.RGBA{R: 10, G: 20, B: 30, A: 0xff})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := Render(img, tt.cols, tt.rows)
			if tt.wantLines == 0 {
				if out != "" {
					t.Fatalf("Render() = %q, want empty", out)
				}
				return
			}
			lines := strings.Split(out, "\n")
			if len(lines) != tt.wantLines {
				t.Fatalf("Render() produced %d lines, want %d", len(lines), tt.wantLines)
			}
			// Every line must measure exactly cols cells once escapes are
			// discounted, or the Bubbletea layout shifts around it.
			for i, line := range lines {
				if w := ansi.StringWidth(line); w != tt.cols {
					t.Errorf("line %d width = %d, want %d", i, w, tt.cols)
				}
			}
		})
	}
}

func TestRenderNilImage(t *testing.T) {
	if out := Render(nil, 8, 4); out != "" {
		t.Errorf("Render(nil) = %q, want empty", out)
	}
}

func TestRenderColoursAndReset(t *testing.T) {
	img := solid(4, 4, color.RGBA{R: 255, G: 128, B: 64, A: 0xff})
	out := Render(img, 4, 2)

	if !strings.Contains(out, "\x1b[38;2;255;128;64m") {
		t.Error("expected the source colour as foreground")
	}
	if !strings.Contains(out, "\x1b[48;2;255;128;64m") {
		t.Error("expected the source colour as background")
	}
	for i, line := range strings.Split(out, "\n") {
		if !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("line %d does not reset colour", i)
		}
	}
	// A single-colour image should emit its escapes once per line, not per
	// cell: 4 cells but only one foreground sequence.
	if n := strings.Count(out, "\x1b[38;2;"); n != 2 {
		t.Errorf("foreground escapes = %d, want 2 (one per line)", n)
	}
}

func TestRenderSplitsTopAndBottom(t *testing.T) {
	// Top half red, bottom half blue: one cell row must carry both.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	red := color.RGBA{R: 255, A: 0xff}
	blue := color.RGBA{B: 255, A: 0xff}
	img.Set(0, 0, red)
	img.Set(1, 0, red)
	img.Set(0, 1, blue)
	img.Set(1, 1, blue)

	out := Render(img, 2, 1)
	if !strings.Contains(out, "\x1b[38;2;255;0;0m") {
		t.Error("top pixel should be the foreground colour")
	}
	if !strings.Contains(out, "\x1b[48;2;0;0;255m") {
		t.Error("bottom pixel should be the background colour")
	}
}

func TestLoadFileURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cover.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, solid(8, 8, color.RGBA{G: 200, A: 0xff})); err != nil {
		t.Fatal(err)
	}
	f.Close()

	img, err := Load(t.Context(), "file://"+path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := img.Bounds().Dx(); got != 8 {
		t.Errorf("decoded width = %d, want 8", got)
	}
}

func TestLoadHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("request carried no User-Agent")
		}
		png.Encode(w, solid(6, 6, color.RGBA{B: 180, A: 0xff}))
	}))
	defer srv.Close()

	img, err := Load(t.Context(), srv.URL+"/cover.png")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := img.Bounds().Dx(); got != 6 {
		t.Errorf("decoded width = %d, want 6", got)
	}
}

func TestLoadErrors(t *testing.T) {
	notImage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not an image"))
	}))
	defer notImage.Close()

	missing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer missing.Close()

	tests := []struct {
		name string
		src  string
	}{
		{"empty source", ""},
		{"missing file", "file:///definitely/not/here.png"},
		{"undecodable body", notImage.URL},
		{"http error status", missing.URL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(t.Context(), tt.src); err == nil {
				t.Error("Load() succeeded, want an error")
			}
		})
	}
}

func TestLoadHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Load(ctx, "http://example.invalid/cover.jpg"); err == nil {
		t.Error("Load() with a cancelled context succeeded, want an error")
	}
}
