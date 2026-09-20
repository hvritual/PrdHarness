//go:build !windows

package filestore

import "os"

func atomicReplace(src, dst string) error { return os.Rename(src, dst) }
func syncDirectory(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
