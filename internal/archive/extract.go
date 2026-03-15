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

func Extract(srcArchive, dstDir string) error {
	if err := os.RemoveAll(dstDir); err != nil {
		return fmt.Errorf("clear extract dir: %w", err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}

	switch {
	case strings.HasSuffix(strings.ToLower(srcArchive), ".zip"):
		return extractZip(srcArchive, dstDir)
	case strings.HasSuffix(strings.ToLower(srcArchive), ".tar.gz"),
		strings.HasSuffix(strings.ToLower(srcArchive), ".tgz"):
		return extractTarGz(srcArchive, dstDir)
	case strings.HasSuffix(strings.ToLower(srcArchive), ".tar"):
		return extractTar(srcArchive, dstDir)
	default:
		return fmt.Errorf("unsupported archive format: %s", srcArchive)
	}
}

func extractZip(srcArchive, dstDir string) error {
	reader, err := zip.OpenReader(srcArchive)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer reader.Close()

	for _, f := range reader.File {
		target := filepath.Join(dstDir, f.Name)
		if err := ensureWithin(dstDir, target); err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", target, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create parent dir for %s: %w", target, err)
		}

		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zipped file %s: %w", f.Name, err)
		}

		dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = src.Close()
			return fmt.Errorf("create extracted file %s: %w", target, err)
		}

		if _, err := io.Copy(dst, src); err != nil {
			_ = dst.Close()
			_ = src.Close()
			return fmt.Errorf("write extracted file %s: %w", target, err)
		}

		_ = dst.Close()
		_ = src.Close()
	}
	return nil
}

func extractTarGz(srcArchive, dstDir string) error {
	file, err := os.Open(srcArchive)
	if err != nil {
		return fmt.Errorf("open tar.gz: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open gzip stream: %w", err)
	}
	defer gzReader.Close()

	return extractTarStream(tar.NewReader(gzReader), dstDir)
}

func extractTar(srcArchive, dstDir string) error {
	file, err := os.Open(srcArchive)
	if err != nil {
		return fmt.Errorf("open tar: %w", err)
	}
	defer file.Close()

	return extractTarStream(tar.NewReader(file), dstDir)
}

func extractTarStream(tr *tar.Reader, dstDir string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read tar entry: %w", err)
		}

		target := filepath.Join(dstDir, hdr.Name)
		if err := ensureWithin(dstDir, target); err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create dir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("create parent dir for %s: %w", target, err)
			}
			dst, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("create file %s: %w", target, err)
			}
			if _, err := io.Copy(dst, tr); err != nil {
				_ = dst.Close()
				return fmt.Errorf("write file %s: %w", target, err)
			}
			_ = dst.Close()
		}
	}
}

func ensureWithin(root, target string) error {
	cleanRoot := filepath.Clean(root)
	cleanTarget := filepath.Clean(target)
	if cleanTarget == cleanRoot {
		return nil
	}
	prefix := cleanRoot + string(filepath.Separator)
	if !strings.HasPrefix(cleanTarget, prefix) {
		return fmt.Errorf("archive entry path escapes target directory: %s", target)
	}
	return nil
}
