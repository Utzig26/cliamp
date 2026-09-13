// Package ytdlcookies stores cookie sources for yt-dlp-backed hosts.
package ytdlcookies

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
)

// Source is where yt-dlp reads cookies for a host: a browser profile
// (--cookies-from-browser) or a Netscape-format cookies.txt (--cookies). When
// both are set the file wins, since it needs no browser on the machine.
type Source struct {
	Browser string // yt-dlp browser spec, e.g. "firefox" or "chrome:Profile 1"
	File    string // path to a cookies.txt exported from a browser or by yt-dlp
}

// IsZero reports whether the source names neither a browser nor a file.
func (s Source) IsZero() bool { return s == Source{} }

// Prepare returns the yt-dlp flags that select this source (nil for a zero
// Source) and a cleanup function to call once that yt-dlp process has exited.
// A file source is copied to a private temporary file first: yt-dlp rewrites
// its --cookies file when it exits (and fails if it cannot), so handing every
// process the user's export would race concurrent invocations against each
// other and break read-only files. A file that cannot be read or copied is an
// error rather than a fallback: yt-dlp treats a missing --cookies path as an
// empty jar and creates the file on exit, which would silently run
// unauthenticated and, for a path yt-dlp expands itself (~), rewrite the
// user's export. cleanup is never nil.
func (s Source) Prepare() (args []string, cleanup func(), err error) {
	cleanup = func() {}
	if s.File == "" {
		if s.Browser != "" {
			return []string{"--cookies-from-browser", s.Browser}, cleanup, nil
		}
		return nil, cleanup, nil
	}
	data, err := os.ReadFile(s.File)
	if err != nil {
		return nil, cleanup, fmt.Errorf("read cookies file: %w", err)
	}
	dir, err := copyDir()
	if err != nil {
		return nil, cleanup, fmt.Errorf("copy cookies file: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "*.txt")
	if err != nil {
		return nil, cleanup, fmt.Errorf("copy cookies file: %w", err)
	}
	path := tmp.Name()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		os.Remove(path)
		return nil, cleanup, fmt.Errorf("copy cookies file: %w", errors.Join(werr, cerr))
	}
	return []string{"--cookies", path}, func() { os.Remove(path) }, nil
}

var (
	dirMu sync.Mutex
	dir   string // session directory for private cookie copies; "" until first use
)

// copyDir returns the session directory that holds private cookie copies,
// creating it on first use (or again if a temp cleaner removed it). It lives
// under os.TempDir with a random name and mode 0700, and Shutdown removes it
// whole, so a copy whose process never reported exit still disappears at exit.
func copyDir() (string, error) {
	dirMu.Lock()
	defer dirMu.Unlock()
	if dir != "" {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir, nil
		}
	}
	d, err := os.MkdirTemp("", "cliamp-cookies-")
	if err != nil {
		return "", err
	}
	dir = d
	return dir, nil
}

// Shutdown removes the session directory and every private cookie copy in
// it. Call it last at exit, after the yt-dlp processes using the copies have
// been joined. A later Prepare starts a fresh directory.
func Shutdown() {
	dirMu.Lock()
	defer dirMu.Unlock()
	if dir != "" {
		os.RemoveAll(dir)
		dir = ""
	}
}

var (
	mu     sync.RWMutex
	byHost = make(map[string]Source)
)

// SetForHost associates a cookie source with a URL host. Passing a zero
// Source removes the association.
func SetForHost(host string, src Source) {
	host = normalizeHost(host)
	if host == "" {
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if src.IsZero() {
		delete(byHost, host)
		return
	}
	byHost[host] = src
}

// ForURL returns the cookie source associated with rawURL. yt-dlp search
// prefixes are mapped to the service host they query.
func ForURL(rawURL string) Source {
	host := hostForURL(rawURL)
	if host == "" {
		return Source{}
	}

	mu.RLock()
	defer mu.RUnlock()
	return byHost[host]
}

func hostForURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	lower := strings.ToLower(rawURL)
	switch {
	case strings.HasPrefix(lower, "scsearch"):
		return "soundcloud.com"
	case strings.HasPrefix(lower, "ytsearch"):
		return "youtube.com"
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return normalizeHost(u.Hostname())
}

func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimPrefix(host, "www.")
	host = strings.TrimPrefix(host, "m.")
	return host
}
