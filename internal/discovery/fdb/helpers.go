package fdb

import (
	"os"

	"github.com/lanory/lanory/internal/discovery"
)

// Result construction helpers and small sysfs wrappers.

// withFDB attaches an FDB observation to a result and returns it, so call sites
// can stay on one line.
func withFDB(r *discovery.Result, o *discovery.FDBObservation) *discovery.Result {
	r.FDBEntry = o
	return r
}

// withLink attaches a link observation to a result and returns it.
func withLink(r *discovery.Result, o *discovery.LinkObservation) *discovery.Result {
	r.Link = o
	return r
}

// readDirNames returns the entry names of dir, skipping subdirectories.
func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// statPath reports whether path exists.
func statPath(path string) (os.FileInfo, error) { return os.Stat(path) }
