package fileutil

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandHome replaces a leading "~" or "~/" with the user's home directory.
// HOME is honored before os.UserHomeDir, matching appdir, so the two agree on
// Windows where os.UserHomeDir reads USERPROFILE. The path is returned
// unchanged when it does not start with ~ or when no home directory is known.
func ExpandHome(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, ok := os.LookupEnv("HOME")
	if !ok || home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return path
		}
	}
	return filepath.Join(home, path[1:])
}
