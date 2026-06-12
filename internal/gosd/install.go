// Package gosd: install / download routines for the stable-diffusion.cpp
// sd-server binary bundle.
//
// stable-diffusion.cpp publishes prebuilt zip archives on its GitHub Releases
// page (https://github.com/leejet/stable-diffusion.cpp/releases) named
//
//	sd-master-<short-sha>-bin-<platform-arch-flavor>.zip
//
// Each archive contains a `sd-server` (or `sd-server.exe`) binary alongside
// the diffusion / ggml shared libraries it dynamically loads. We do not
// attempt to enumerate every flavor — pickAssetSubstring picks a single
// sensible default per (GOOS, GOARCH) that maximises portability.
//
// The matched substring is treated as case-insensitive. Users wanting GPU
// builds (CUDA/ROCm/Vulkan) can drop a custom build into LibPath manually;
// EnsureReady detects an existing bundle and skips the download.
package gosd

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// serverBinaryName returns the platform-specific filename of the sd-server
// executable shipped inside the upstream release zip.
func serverBinaryName() string {
	if runtime.GOOS == "windows" {
		return "sd-server.exe"
	}
	return "sd-server"
}

// bundlePresent reports whether the sd-server binary is already extracted
// inside dir. We accept both a flat layout (binary at the root) and a
// nested layout (binary one level deep, e.g. build/bin/sd-server).
func bundlePresent(dir string) bool {
	if dir == "" {
		return false
	}
	name := serverBinaryName()
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		return true
	}
	matched := false
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Name() == name {
			matched = true
			return io.EOF
		}
		return nil
	})
	return matched
}

// installBundle resolves the latest sd.cpp release tag (or honours the
// SD_CPP_VERSION env override), picks the asset matching the host, downloads
// it and unzips it into dir.
func installBundle(ctx context.Context, dir string, progress ProgressFunc) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("gosd: mkdir lib: %w", err)
	}

	tag, err := resolveTag(ctx)
	if err != nil {
		return err
	}
	asset, err := resolveAsset(ctx, tag)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("https://github.com/leejet/stable-diffusion.cpp/releases/download/%s/%s", tag, asset)

	if progress != nil {
		progress("downloading", 0)
	}

	zipPath := filepath.Join(dir, asset)
	if err := downloadFile(ctx, url, zipPath, progress); err != nil {
		return fmt.Errorf("gosd: download %s: %w", url, err)
	}
	defer os.Remove(zipPath)

	if progress != nil {
		progress("extracting", 0.92)
	}
	if err := unzipFlat(zipPath, dir); err != nil {
		return fmt.Errorf("gosd: extract: %w", err)
	}

	if !bundlePresent(dir) {
		return errors.New("gosd: bundle missing sd-server binary after extraction")
	}
	// Stamp the directory with the tag we just installed so EnsureReady can
	// detect and replace mismatched bundles on the next launch.
	_ = os.WriteFile(filepath.Join(dir, ".sd_cpp_version"), []byte(tag+"\n"), 0o644)
	if progress != nil {
		progress("ready", 1.0)
	}
	return nil
}

// installedTag returns the tag stamped into dir at install time, or "" if
// missing (e.g. an older install predating the version stamp).
func installedTag(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, ".sd_cpp_version"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// expectedTag returns the tag we want installed: SD_CPP_VERSION override or
// the pinned default.
func expectedTag() string {
	if v := strings.TrimSpace(os.Getenv("SD_CPP_VERSION")); v != "" {
		return v
	}
	return pinnedSDCPPTag
}

// wipeLibDir removes every entry inside dir but keeps dir itself.
func wipeLibDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// pinnedSDCPPTag is the stable-diffusion.cpp release tag we install by
// default. Users can override via SD_CPP_VERSION. We pin instead of
// chasing "latest" so a release that breaks the sd-server CLI flags we
// rely on doesn't silently break Nurvis.
const pinnedSDCPPTag = "master-679-f3fd359"

// resolveTag returns the release tag to install.
func resolveTag(_ context.Context) (string, error) {
	if v := strings.TrimSpace(os.Getenv("SD_CPP_VERSION")); v != "" {
		return v, nil
	}
	return pinnedSDCPPTag, nil
}

// resolveAsset queries the public expanded_assets HTML fragment (no API auth
// required) and picks the asset whose filename contains all hints for the
// current host platform.
func resolveAsset(ctx context.Context, tag string) (string, error) {
	url := fmt.Sprintf("https://github.com/leejet/stable-diffusion.cpp/releases/expanded_assets/%s", tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gosd: list assets HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	html := string(body)

	var (
		picks   []string
		needles = pickAssetHints()
	)
	for {
		i := strings.Index(html, "sd-master-")
		if i < 0 {
			break
		}
		j := strings.Index(html[i:], ".zip")
		if j < 0 {
			break
		}
		name := html[i : i+j+4]
		picks = append(picks, name)
		html = html[i+j+4:]
	}
	if len(picks) == 0 {
		return "", fmt.Errorf("gosd: no .zip assets found for tag %s", tag)
	}

nextPick:
	for _, p := range picks {
		lower := strings.ToLower(p)
		for _, n := range needles {
			if !strings.Contains(lower, strings.ToLower(n)) {
				continue nextPick
			}
		}
		return p, nil
	}
	return "", fmt.Errorf("gosd: no asset matches host (%s/%s) hints %v among %d candidates",
		runtime.GOOS, runtime.GOARCH, needles, len(picks))
}

// pickAssetHints returns substrings the chosen asset filename must contain.
func pickAssetHints() []string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return []string{"darwin", "arm64"}
	case "darwin/amd64":
		return []string{"darwin", "x86_64"}
	case "linux/amd64":
		return []string{"linux", "x86_64"}
	case "linux/arm64":
		return []string{"linux", "arm64"}
	case "windows/amd64":
		return []string{"win", "avx2"}
	default:
		return []string{strings.ToLower(runtime.GOOS), strings.ToLower(runtime.GOARCH)}
	}
}

// downloadFile streams url → dst with progress callbacks.
func downloadFile(ctx context.Context, url, dst string, progress ProgressFunc) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 64*1024)
	lastReport := time.Now()
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			written += int64(n)
			if progress != nil && total > 0 && time.Since(lastReport) > 250*time.Millisecond {
				lastReport = time.Now()
				progress("downloading", float64(written)/float64(total)*0.9)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return nil
}

// unzipFlat extracts every regular file from zipPath into destDir, ignoring
// any internal directory hierarchy. Filenames colliding under the flat
// layout are replaced (last-write-wins).
func unzipFlat(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.FileInfo().IsDir() {
			continue
		}
		base := filepath.Base(f.Name)
		if base == "" || strings.HasPrefix(base, ".") {
			continue
		}
		out := filepath.Join(destDir, base)
		if err := writeZipFile(f, out); err != nil {
			return err
		}
	}
	return nil
}

func writeZipFile(zf *zip.File, dst string) error {
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	w, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode()|0o644)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, rc)
	return err
}
