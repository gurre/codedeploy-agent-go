package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// testOS returns the appropriate OS value for test appspecs based on runtime.
// Use this in tests instead of hardcoding "os: linux" to ensure tests pass on all platforms.
func testOS() string {
	if runtime.GOOS == "windows" {
		return "windows"
	}
	return "linux"
}

// TestUnpackTar verifies tar extraction produces the expected files.
func TestUnpackTar(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar")
	createTarFile(t, tarPath, false, map[string]string{
		"appspec.yml":        fmt.Sprintf("version: 0.0\nos: %s\n", testOS()),
		"scripts/install.sh": "#!/bin/bash\necho hello\n",
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "tar"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	assertFileExists(t, filepath.Join(destDir, "appspec.yml"))
	assertFileExists(t, filepath.Join(destDir, "scripts/install.sh"))
}

// TestUnpackTgz verifies gzipped tar extraction.
func TestUnpackTgz(t *testing.T) {
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "bundle.tgz")
	createTarFile(t, tgzPath, true, map[string]string{
		"appspec.yml": fmt.Sprintf("version: 0.0\nos: %s\n", testOS()),
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tgzPath, destDir, "tgz"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	assertFileExists(t, filepath.Join(destDir, "appspec.yml"))
}

// TestUnpackZip verifies zip extraction.
func TestUnpackZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "bundle.zip")
	createZipFile(t, zipPath, map[string]string{
		"appspec.yml": fmt.Sprintf("version: 0.0\nos: %s\n", testOS()),
		"config.txt":  "key=value\n",
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(zipPath, destDir, "zip"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	assertFileExists(t, filepath.Join(destDir, "appspec.yml"))
	assertFileExists(t, filepath.Join(destDir, "config.txt"))
}

// TestStripLeadingDirectory verifies that a tar with a single top-level
// directory containing an appspec file gets stripped. This is how GitHub
// tarballs are structured.
func TestStripLeadingDirectory(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar")
	createTarFile(t, tarPath, false, map[string]string{
		"myapp-v1/appspec.yml":        fmt.Sprintf("version: 0.0\nos: %s\n", testOS()),
		"myapp-v1/scripts/install.sh": "#!/bin/bash\n",
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "tar"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	// After stripping, appspec should be at root level
	assertFileExists(t, filepath.Join(destDir, "appspec.yml"))
	assertFileExists(t, filepath.Join(destDir, "scripts/install.sh"))
}

// TestNoStripWithoutAppspec verifies that leading directory is NOT stripped
// when there's no appspec file inside. This prevents accidentally moving
// files for non-CodeDeploy archives.
func TestNoStripWithoutAppspec(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar")
	createTarFile(t, tarPath, false, map[string]string{
		"myapp/readme.txt": "hello\n",
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "tar"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	// Should NOT strip - myapp dir should remain
	assertFileExists(t, filepath.Join(destDir, "myapp/readme.txt"))
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("expected file %q to exist", path)
	}
}

func createTarFile(t *testing.T, path string, gzipped bool, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var tw *tar.Writer
	if gzipped {
		gw := gzip.NewWriter(f)
		defer func() { _ = gw.Close() }()
		tw = tar.NewWriter(gw)
	} else {
		tw = tar.NewWriter(f)
	}
	defer func() { _ = tw.Close() }()

	// Collect directories
	dirs := make(map[string]bool)
	for name := range files {
		d := filepath.Dir(name)
		for d != "." && d != "/" {
			dirs[d] = true
			d = filepath.Dir(d)
		}
	}
	for d := range dirs {
		_ = tw.WriteHeader(&tar.Header{
			Name:     d + "/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		})
	}

	for name, content := range files {
		_ = tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		})
		_, _ = tw.Write([]byte(content))
	}
}

// TestUnpackDefaultBundleType verifies that an unknown bundle type
// falls through to tar extraction (matches Ruby behavior).
func TestUnpackDefaultBundleType(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "bundle.tar")
	createTarFile(t, tarPath, false, map[string]string{
		"appspec.yml": fmt.Sprintf("version: 0.0\nos: %s\n", testOS()),
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "unknown"); err != nil {
		t.Fatalf("Unpack with unknown type: %v", err)
	}

	assertFileExists(t, filepath.Join(destDir, "appspec.yml"))
}

// TestUnpackInvalidArchive verifies that a corrupt archive returns an error
// rather than producing partial output.
func TestUnpackInvalidArchive(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.tar")
	if err := os.WriteFile(badPath, []byte("not a tar file"), 0o644); err != nil {
		t.Fatal(err)
	}

	u := NewUnpacker()
	if err := u.Unpack(badPath, filepath.Join(dir, "out"), "tar"); err == nil {
		t.Fatal("expected error for invalid archive")
	}
}

// TestUnpackInvalidGzip verifies that a corrupt gzip file returns an error.
func TestUnpackInvalidGzip(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.tgz")
	if err := os.WriteFile(badPath, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	u := NewUnpacker()
	if err := u.Unpack(badPath, filepath.Join(dir, "out"), "tgz"); err == nil {
		t.Fatal("expected error for invalid gzip")
	}
}

// TestUnpackInvalidZip verifies that a corrupt zip file returns an error.
func TestUnpackInvalidZip(t *testing.T) {
	dir := t.TempDir()
	badPath := filepath.Join(dir, "bad.zip")
	if err := os.WriteFile(badPath, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}

	u := NewUnpacker()
	if err := u.Unpack(badPath, filepath.Join(dir, "out"), "zip"); err == nil {
		t.Fatal("expected error for invalid zip")
	}
}

// TestUnpackMissingArchive verifies that a non-existent archive path
// returns an error.
func TestUnpackMissingArchive(t *testing.T) {
	u := NewUnpacker()
	if err := u.Unpack("/nonexistent/archive.tar", "/tmp/out", "tar"); err == nil {
		t.Fatal("expected error for missing archive")
	}
}

// TestTarPathTraversal verifies that archives containing path traversal
// entries (../) are rejected. This prevents arbitrary file writes outside
// the destination directory.
func TestTarPathTraversal(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "traversal.tar")
	createTarWithTraversal(t, tarPath)

	u := NewUnpacker()
	err := u.Unpack(tarPath, filepath.Join(dir, "out"), "tar")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

// TestZipPathTraversal verifies that zip archives with path traversal
// entries are rejected.
func TestZipPathTraversal(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "traversal.zip")
	createZipWithTraversal(t, zipPath)

	u := NewUnpacker()
	err := u.Unpack(zipPath, filepath.Join(dir, "out"), "zip")
	if err == nil {
		t.Fatal("expected error for path traversal in zip")
	}
}

// TestTarSymlink verifies that symlinks within tar archives are
// extracted correctly.
func TestTarSymlink(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "symlink.tar")
	createTarWithSymlink(t, tarPath)

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "tar"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	link := filepath.Join(destDir, "link.txt")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if target != "real.txt" {
		t.Errorf("symlink target = %q, want real.txt", target)
	}
}

// TestZipDirectory verifies that zip entries marked as directories are
// created properly during extraction.
func TestZipDirectory(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "dirs.zip")
	createZipWithDirectory(t, zipPath)

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(zipPath, destDir, "zip"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	info, err := os.Stat(filepath.Join(destDir, "subdir"))
	if err != nil {
		t.Fatalf("stat subdir: %v", err)
	}
	if !info.IsDir() {
		t.Error("subdir should be a directory")
	}
	assertFileExists(t, filepath.Join(destDir, "subdir", "file.txt"))
}

// TestMultipleTopLevelEntriesNoStrip verifies that archives with multiple
// top-level entries are not subject to leading directory stripping.
func TestMultipleTopLevelEntriesNoStrip(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "multi.tar")
	createTarFile(t, tarPath, false, map[string]string{
		"file1.txt": "one\n",
		"file2.txt": "two\n",
	})

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "tar"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	assertFileExists(t, filepath.Join(destDir, "file1.txt"))
	assertFileExists(t, filepath.Join(destDir, "file2.txt"))
}

// TestWriteFileZeroMode verifies that files with mode 0 get defaulted
// to 0644 rather than being created with no permissions.
func TestWriteFileZeroMode(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "zeromode.tar")
	createTarWithZeroMode(t, tarPath)

	destDir := filepath.Join(dir, "out")
	u := NewUnpacker()
	if err := u.Unpack(tarPath, destDir, "tar"); err != nil {
		t.Fatalf("Unpack: %v", err)
	}

	info, err := os.Stat(filepath.Join(destDir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", info.Mode().Perm())
	}
}

func createZipFile(t *testing.T, path string, files map[string]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	w := zip.NewWriter(f)
	defer func() { _ = w.Close() }()

	for name, content := range files {
		fw, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(content))
	}
}

func createTarWithTraversal(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	tw := tar.NewWriter(f)
	defer func() { _ = tw.Close() }()

	content := []byte("malicious")
	_ = tw.WriteHeader(&tar.Header{
		Name: "../../../etc/evil.txt",
		Mode: 0o644,
		Size: int64(len(content)),
	})
	_, _ = tw.Write(content)
}

func createZipWithTraversal(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	w := zip.NewWriter(f)
	defer func() { _ = w.Close() }()

	fw, err := w.Create("../../../etc/evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("malicious"))
}

func createTarWithSymlink(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	tw := tar.NewWriter(f)
	defer func() { _ = tw.Close() }()

	content := []byte("hello")
	_ = tw.WriteHeader(&tar.Header{
		Name: "real.txt",
		Mode: 0o644,
		Size: int64(len(content)),
	})
	_, _ = tw.Write(content)

	_ = tw.WriteHeader(&tar.Header{
		Name:     "link.txt",
		Typeflag: tar.TypeSymlink,
		Linkname: "real.txt",
	})
}

func createZipWithDirectory(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	w := zip.NewWriter(f)
	defer func() { _ = w.Close() }()

	// Create directory entry with proper mode via CreateHeader
	dirHeader := &zip.FileHeader{
		Name: "subdir/",
	}
	dirHeader.SetMode(os.ModeDir | 0o755)
	if _, err := w.CreateHeader(dirHeader); err != nil {
		t.Fatal(err)
	}

	fw, err := w.Create("subdir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write([]byte("inside subdir"))
}

func createTarWithZeroMode(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	tw := tar.NewWriter(f)
	defer func() { _ = tw.Close() }()

	content := []byte("zero mode file")
	_ = tw.WriteHeader(&tar.Header{
		Name: "file.txt",
		Mode: 0, // intentionally zero
		Size: int64(len(content)),
	})
	_, _ = tw.Write(content)
}
