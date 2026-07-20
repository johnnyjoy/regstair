package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type backupManifest struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	Contents  []string  `json:"contents"`
	KeyNotice string    `json:"key_notice"`
}

func runAdminBackup(args []string) error {
	flags := flag.NewFlagSet("regstair admin backup", flag.ContinueOnError)
	var contentRoot, configPath, output string
	flags.StringVar(&contentRoot, "content-root", "/var/lib/regstair/content", "offline Regstair content root")
	flags.StringVar(&configPath, "config", "/etc/regstair/regstair.yaml", "authoritative Regstair YAML configuration")
	flags.StringVar(&output, "output", "", "new .tar.gz backup path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if contentRoot == "" || configPath == "" || output == "" {
		return fmt.Errorf("content-root, config, and output are required")
	}
	if _, err := os.Stat(output); err == nil || !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("backup output must not already exist")
	}
	if info, err := os.Stat(contentRoot); err != nil || !info.IsDir() {
		return fmt.Errorf("content root is unavailable")
	}
	if info, err := os.Stat(configPath); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("configuration file is unavailable")
	}
	file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create backup: %w", err)
	}
	succeeded := false
	defer func() {
		_ = file.Close()
		if !succeeded {
			_ = os.Remove(output)
		}
	}()
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	manifest := backupManifest{Version: 1, CreatedAt: time.Now().UTC(), Contents: []string{"config/regstair.yaml", "content/"}, KeyNotice: "Credential encryption keys are intentionally excluded and must be backed up separately."}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err == nil {
		err = writeTarBytes(tw, "manifest.json", 0o600, encoded)
	}
	if err == nil {
		err = writeTarFile(tw, configPath, "config/regstair.yaml")
	}
	if err == nil {
		err = filepath.WalkDir(contentRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(contentRoot, path)
			if err != nil || relative == "." {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("backup refuses symbolic link %q", relative)
			}
			return writeTarFile(tw, path, filepath.ToSlash(filepath.Join("content", relative)))
		})
	}
	if closeErr := tw.Close(); err == nil {
		err = closeErr
	}
	if closeErr := gz.Close(); err == nil {
		err = closeErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	succeeded = true
	return nil
}

func runAdminRestore(args []string) error {
	flags := flag.NewFlagSet("regstair admin restore", flag.ContinueOnError)
	var archive, contentRoot, configOutput string
	flags.StringVar(&archive, "archive", "", "Regstair .tar.gz backup")
	flags.StringVar(&contentRoot, "content-root", "/var/lib/regstair/content", "new empty content root")
	flags.StringVar(&configOutput, "config-output", "", "new restored YAML path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if archive == "" || contentRoot == "" || configOutput == "" {
		return fmt.Errorf("archive, content-root, and config-output are required")
	}
	if !pathAbsentOrEmptyDir(contentRoot) {
		return fmt.Errorf("restore content root must be absent or empty")
	}
	if _, err := os.Stat(configOutput); err == nil || !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("restore configuration output must not exist")
	}
	file, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open compressed backup: %w", err)
	}
	defer gz.Close()
	if err := os.MkdirAll(contentRoot, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configOutput), 0o700); err != nil {
		return err
	}
	seenManifest, seenConfig := false, false
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read backup: %w", err)
		}
		name := filepath.ToSlash(filepath.Clean(header.Name))
		if name == "." || strings.HasPrefix(name, "../") || filepath.IsAbs(header.Name) {
			return fmt.Errorf("backup contains unsafe path")
		}
		switch {
		case name == "manifest.json":
			var manifest backupManifest
			if err := json.NewDecoder(io.LimitReader(tr, 64<<10)).Decode(&manifest); err != nil || manifest.Version != 1 {
				return fmt.Errorf("backup manifest is invalid")
			}
			seenManifest = true
		case name == "config/regstair.yaml":
			if err := restoreTarFile(tr, header, configOutput); err != nil {
				return err
			}
			seenConfig = true
		case strings.HasPrefix(name, "content/"):
			relative := strings.TrimPrefix(name, "content/")
			if relative == "" {
				continue
			}
			target := filepath.Join(contentRoot, filepath.FromSlash(relative))
			if err := restoreTarFile(tr, header, target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("backup contains unexpected entry %q", name)
		}
	}
	if !seenManifest || !seenConfig {
		return fmt.Errorf("backup is incomplete")
	}
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, mode fs.FileMode, contents []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: int64(mode.Perm()), Size: int64(len(contents)), ModTime: time.Now().UTC()}); err != nil {
		return err
	}
	_, err := tw.Write(contents)
	return err
}

func writeTarFile(tw *tar.Writer, source, name string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return err
	}
	header.Name = name
	if err := tw.WriteHeader(header); err != nil || info.IsDir() {
		return err
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(tw, file)
	return err
}

func restoreTarFile(reader io.Reader, header *tar.Header, target string) error {
	switch header.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o700)
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fs.FileMode(header.Mode)&0o700)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, reader, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	default:
		return fmt.Errorf("backup contains unsupported entry type")
	}
}

func pathAbsentOrEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && len(entries) == 0
}
