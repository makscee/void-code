package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

// copyTree deliberately uses Lstat and recreates links verbatim. It never follows
// a link in the immutable fixture bundle.
func copyTree(source, destination string) error {
	root, err := os.Lstat(source)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return errors.New("copy source is not a real directory")
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		to := filepath.Join(destination, rel)
		mode := info.Mode()
		switch {
		case mode.IsDir():
			if err := os.Mkdir(to, mode.Perm()); err != nil && !os.IsExist(err) {
				return err
			}
			return os.Chmod(to, mode.Perm())
		case mode&os.ModeSymlink != 0:
			value, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(value, to)
		case mode.IsRegular():
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
			if err != nil {
				in.Close()
				return err
			}
			_, copyErr := io.Copy(out, in)
			closeInErr := in.Close()
			syncErr := out.Sync()
			closeOutErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeInErr != nil {
				return closeInErr
			}
			if syncErr != nil {
				return syncErr
			}
			if closeOutErr != nil {
				return closeOutErr
			}
			return os.Chmod(to, mode.Perm())
		default:
			return errors.New("unsupported bundle entry")
		}
	})
}
