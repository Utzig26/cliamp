package radio

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestFavoritesAddRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radio_favorites.toml")

	f := &Favorites{byURL: make(map[string]struct{}), path: path}

	s := CatalogStation{
		Name:    "Test FM",
		URL:     "https://test.example.com/stream",
		Country: "Norway",
		Bitrate: 128,
	}

	// Add
	if err := f.Add(s); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !f.Contains(s.URL) {
		t.Fatal("expected Contains to return true after Add")
	}
	if len(f.Stations()) != 1 {
		t.Fatalf("expected 1 station, got %d", len(f.Stations()))
	}

	// Add duplicate should be no-op
	if err := f.Add(s); err != nil {
		t.Fatalf("Add duplicate: %v", err)
	}
	if len(f.Stations()) != 1 {
		t.Fatalf("expected 1 station after duplicate add, got %d", len(f.Stations()))
	}

	// Verify persistence
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty favorites file")
	}

	// Reload from disk
	stations, err := loadFavoriteStations(path)
	if err != nil {
		t.Fatalf("loadFavoriteStations: %v", err)
	}
	if len(stations) != 1 || stations[0].Name != "Test FM" {
		t.Fatalf("unexpected reloaded stations: %+v", stations)
	}

	// Remove
	if err := f.Remove(s.URL); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if f.Contains(s.URL) {
		t.Fatal("expected Contains to return false after Remove")
	}
	if len(f.Stations()) != 0 {
		t.Fatalf("expected 0 stations after remove, got %d", len(f.Stations()))
	}

	// Remove non-existent should be no-op
	if err := f.Remove("https://nonexistent.example.com"); err != nil {
		t.Fatalf("Remove non-existent: %v", err)
	}
}

func TestFavoritesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "radio_favorites.toml")

	f := &Favorites{byURL: make(map[string]struct{}), path: path}

	stations := []CatalogStation{
		{Name: "Jazz FM", URL: "https://jazz.example.com/stream", Country: "UK", State: "Région \"North\"", Bitrate: 320, Codec: "mp3", Homepage: "https://jazz.example.com"},
		{Name: "Rock Radio", URL: "https://rock.example.com/stream", Country: "US", Bitrate: 192, Tags: "rock,metal"},
	}
	for _, s := range stations {
		if err := f.Add(s); err != nil {
			t.Fatalf("Add %s: %v", s.Name, err)
		}
	}

	// Reload
	loaded, err := loadFavoriteStations(path)
	if err != nil {
		t.Fatalf("loadFavoriteStations: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 stations, got %d", len(loaded))
	}
	if !slices.Equal(loaded, stations) {
		t.Fatalf("unexpected loaded data: %+v", loaded)
	}
}

func TestFavoritesToggleSharedWithProvider(t *testing.T) {
	t.Setenv("CLIAMP_CONFIG_DIR", t.TempDir())
	favorites := LoadFavorites()
	p := New(Options{Favorites: favorites, Country: CountryDeclined})
	station := CatalogStation{Name: "Jazz FM", URL: "https://jazz.example/stream"}
	p.AppendCatalog([]CatalogStation{station})

	// The Catalog adapter and playback share one store, keyed by URL.
	if added, _, err := p.ToggleFavorite("c:0"); err != nil || !added {
		t.Fatalf("catalog toggle = %v, %v", added, err)
	}
	station.Name = "Different directory label"
	if added, err := favorites.Toggle(station); err != nil || added {
		t.Fatalf("track toggle = %v, %v; want removal by URL", added, err)
	}
	if favorites.Count() != 0 || LoadFavorites().Count() != 0 {
		t.Fatal("removal did not reach the shared store and disk")
	}
	if added, err := favorites.Toggle(station); err != nil || !added {
		t.Fatalf("track toggle = %v, %v", added, err)
	}
	lists, err := p.Playlists()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, list := range lists {
		if list.ID == "f:"+station.URL {
			found = strings.Contains(list.Name, station.Name)
		}
	}
	if !found || !LoadFavorites().Contains(station.URL) {
		t.Fatal("playback favorite missing from provider list or reloaded store")
	}

	// Callers cannot mutate the live store through a returned slice.
	snapshot := favorites.Stations()
	snapshot[0].Name = "Corrupted"
	if favorites.Stations()[0].Name != station.Name {
		t.Fatal("Stations returned mutable store state")
	}
}

func TestFavoritesFailedWritePreservesState(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprintf("remove=%v", remove), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), favoritesFile)
			f := &Favorites{path: path}
			station := CatalogStation{Name: "Jazz", URL: "https://jazz.example/stream"}
			if remove {
				if err := f.Add(station); err != nil {
					t.Fatal(err)
				}
			}
			// Renaming a regular file over a directory fails even when run as root.
			f.path = t.TempDir()
			revision := f.Revision()
			if _, err := f.Toggle(station); err == nil {
				t.Fatal("expected persistence failure")
			}
			if f.Revision() != revision {
				t.Fatal("failed write advanced revision")
			}
			if f.Contains(station.URL) != remove || (f.Count() == 1) != remove {
				t.Fatal("failed write changed memory")
			}
		})
	}
}

func TestFavoritesConcurrentAccess(t *testing.T) {
	f := &Favorites{path: filepath.Join(t.TempDir(), favoritesFile)}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			station := CatalogStation{Name: fmt.Sprintf("Station %d", i), URL: fmt.Sprintf("https://radio.example/%d", i)}
			for range 4 {
				if _, err := f.Toggle(station); err != nil {
					t.Error(err)
				}
				f.Contains(station.URL)
				f.Stations()
				f.Count()
			}
		})
	}
	wg.Wait()
	if f.Count() != 0 {
		t.Fatalf("unbalanced toggles: %v", f.Stations())
	}
}

func TestFavoritesRevision(t *testing.T) {
	f := &Favorites{path: filepath.Join(t.TempDir(), favoritesFile)}
	station := CatalogStation{Name: "Jazz", URL: "https://jazz.example/stream"}
	for _, tc := range []struct {
		name   string
		mutate func() error
		want   uint64
	}{
		{"add", func() error { return f.Add(station) }, 1},
		{"duplicate add", func() error { return f.Add(station) }, 1},
		{"absent removal", func() error { return f.Remove("https://absent.example") }, 1},
		{"remove", func() error { return f.Remove(station.URL) }, 2},
		{"toggle add", func() error { _, err := f.Toggle(station); return err }, 3},
		{"toggle remove", func() error { _, err := f.Toggle(station); return err }, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(); err != nil {
				t.Fatal(err)
			}
			if got := f.Revision(); got != tc.want {
				t.Fatalf("revision = %d, want %d", got, tc.want)
			}
		})
	}
}
