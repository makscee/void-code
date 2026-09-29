package codexruntime

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// extractFile unpacks the .tar.gz at archivePath into root.
func extractFile(archivePath, root string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	return extract(tar.NewReader(gz), root)
}

// extract writes every entry under root. Entries must stay inside root:
// absolute names, "..", symlinks pointing outside, hard links and writes
// through an already unpacked symlink are all refused.
func extract(tr *tar.Reader, root string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if err := extractEntry(tr, hdr, root); err != nil {
			return err
		}
	}
}

func extractEntry(tr *tar.Reader, hdr *tar.Header, root string) error {
	if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
		return nil
	}
	if rooted(hdr.Name) {
		return fmt.Errorf("entry %q leaves the Codex folder", hdr.Name)
	}
	rel := filepath.FromSlash(strings.TrimPrefix(hdr.Name, "./"))
	if rel == "" || rel == "." || rel == string(filepath.Separator) {
		return nil
	}
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("entry %q leaves the Codex folder", hdr.Name)
	}
	rel = filepath.Clean(rel)
	if err := refuseSymlinkedParents(root, filepath.Dir(rel)); err != nil {
		return fmt.Errorf("entry %q: %w", hdr.Name, err)
	}
	target := filepath.Join(root, rel)
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0755)
	case tar.TypeReg:
		return writeRegular(tr, target, hdr.Mode)
	case tar.TypeSymlink:
		return writeSymlink(hdr, rel, target)
	}
	return fmt.Errorf("entry %q has unsupported type %q", hdr.Name, string(hdr.Typeflag))
}

// refuseSymlinkedParents fails when any folder on the way to an entry is a
// symlink unpacked earlier. Each link is checked against its own location, and
// a link reached through another link would resolve somewhere that check never
// saw.
func refuseSymlinkedParents(root, relDir string) error {
	if relDir == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(relDir, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path goes through the symlink %s", part)
		}
	}
	return nil
}

func writeRegular(r io.Reader, target string, mode int64) error {
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	perm := os.FileMode(0644)
	if mode&0111 != 0 {
		perm = 0755
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func writeSymlink(hdr *tar.Header, rel, target string) error {
	if rooted(hdr.Linkname) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), filepath.FromSlash(hdr.Linkname))) {
		return fmt.Errorf("symlink %q -> %q leaves the Codex folder", hdr.Name, hdr.Linkname)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return os.Symlink(hdr.Linkname, target)
}

// rooted reports whether a name or link target names a root, a drive or a
// share rather than a relative path. On Windows "/etc/passwd" is not
// filepath.IsAbs (it has no drive) but still points at the drive's root.
func rooted(name string) bool {
	p := filepath.FromSlash(name)
	return path.IsAbs(name) || filepath.IsAbs(p) || filepath.VolumeName(p) != "" ||
		strings.HasPrefix(p, string(filepath.Separator))
}
