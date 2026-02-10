// Package archive provides bundle extraction for tar, tgz, and zip archives.
// It handles the strip-leading-directory logic for archives that wrap their
// contents in a single top-level directory.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Unpacker extracts deployment bundle archives to a destination directory.
type Unpacker struct{}

// NewUnpacker creates an archive unpacker.
//
//	u := archive.NewUnpacker()
//	err := u.Unpack("/path/to/bundle.tgz", "/opt/deploy/archive", "tgz")
func NewUnpacker() *Unpacker {
	return &Unpacker{}
}

// Unpack extracts an archive file to the destination directory based on bundle type.
// Supported types: "tar", "tgz", "zip".
// After extraction, if the archive contains a single top-level directory with an
// appspec file, that directory is stripped (contents moved up one level).
func (u *Unpacker) Unpack(archivePath, destDir, bundleType string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("archive: create dest dir: %w", err)
	}

	switch bundleType {
	case "tar":
		if err := extractTar(archivePath, destDir, false); err != nil {
			return err
		}
	case "tgz":
		if err := extractTar(archivePath, destDir, true); err != nil {
			return err
		}
	case "zip":
		if err := extractZip(archivePath, destDir); err != nil {
			return err
		}
	default:
		// Default to tar (matches Ruby behavior)
		if err := extractTar(archivePath, destDir, false); err != nil {
			return err
		}
	}

	return stripLeadingDirectory(destDir)
}

func extractTar(archivePath, destDir string, gzipped bool) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("archive: open %s: %w", archivePath, err)
	}
	defer func() { _ = f.Close() }()

	var reader io.Reader = f
	if gzipped {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("archive: gzip reader: %w", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}

	tr := tar.NewReader(reader)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("archive: tar read: %w", err)
		}

		target := filepath.Join(destDir, header.Name)

		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) &&
			filepath.Clean(target) != filepath.Clean(destDir) {
			return fmt.Errorf("archive: illegal path %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("archive: mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("archive: mkdir parent: %w", err)
			}
			if err := writeFile(target, tr, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("archive: mkdir parent: %w", err)
			}
			if err := os.Symlink(header.Linkname, target); err != nil {
				return fmt.Errorf("archive: symlink: %w", err)
			}
		}
	}
	return nil
}

func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("archive: open zip %s: %w", archivePath, err)
	}
	defer func() { _ = r.Close() }()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)

		// Prevent path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) &&
			filepath.Clean(target) != filepath.Clean(destDir) {
			return fmt.Errorf("archive: illegal path %q", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, f.Mode()); err != nil {
				return fmt.Errorf("archive: mkdir %s: %w", target, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("archive: mkdir parent: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("archive: open zip entry %s: %w", f.Name, err)
		}
		err = writeFile(target, rc, f.Mode())
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("archive: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("archive: write %s: %w", path, err)
	}
	return nil
}

// stripLeadingDirectory checks if the archive was wrapped in a single
// top-level directory containing an appspec file. If so, moves the contents
// up one level and removes the wrapper directory.
func stripLeadingDirectory(destDir string) error {
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return err
	}

	if len(entries) != 1 || !entries[0].IsDir() {
		return nil
	}

	nestedDir := filepath.Join(destDir, entries[0].Name())
	nestedEntries, err := os.ReadDir(nestedDir)
	if err != nil {
		return err
	}

	// Check if nested directory contains an appspec file
	hasAppspec := false
	for _, e := range nestedEntries {
		lower := strings.ToLower(e.Name())
		if strings.Contains(lower, "appspec") {
			hasAppspec = true
			break
		}
	}

	if !hasAppspec {
		return nil
	}

	// Move contents up: use a temp dir to avoid conflicts
	tmpDir := destDir + "-temp"
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	if err := os.Rename(destDir, tmpDir); err != nil {
		return err
	}

	nestedInTmp := filepath.Join(tmpDir, entries[0].Name())
	if err := os.Rename(nestedInTmp, destDir); err != nil {
		return err
	}

	return os.Remove(tmpDir)
}
