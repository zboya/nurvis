package llamax

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hashicorp/go-getter"
	"github.com/hybridgroup/yzma/pkg/download"
)

// llamaCPPVersion is the llama.cpp release tag whose prebuilt binaries we pull.
//
// Empty string ("") / "latest" lets yzma resolve the current pinned version
// from its build server, with automatic fallback to the previous release if
// the latest is still building. Pin to a concrete `bNNNN` tag here only when
// reproducibility outweighs auto-update.
const llamaCPPVersion = ""

// platformLibName returns the absolute path of a llama.cpp shared library for
// the current OS within the given directory.
func platformLibName(dir, lib string) string {
	switch runtime.GOOS {
	case "linux", "freebsd":
		return filepath.Join(dir, fmt.Sprintf("lib%s.so", lib))
	case "windows":
		return filepath.Join(dir, fmt.Sprintf("%s.dll", lib))
	case "darwin":
		return filepath.Join(dir, fmt.Sprintf("lib%s.dylib", lib))
	default:
		return filepath.Join(dir, lib)
	}
}

// installLibs downloads + extracts the llama.cpp prebuilt library bundle into
// dir using yzma's pkg/download. yzma handles archive selection (CPU / Metal /
// CUDA / Vulkan / ROCm), download with retries, extraction (.zip / .tar.gz),
// and version fallback — we just need to pick the right Processor for this
// host and forward progress events.
func installLibs(ctx context.Context, dir string, progress ProgressFunc) error {
	processor, err := pickProcessor()
	if err != nil {
		return err
	}

	if progress != nil {
		progress("downloading", 0)
	}
	slog.Info("llamax: downloading llama.cpp",
		"os", runtime.GOOS, "arch", runtime.GOARCH,
		"processor", processor.String(), "version", versionLabel(),
	)

	tracker := newProgressTracker(progress)
	if err := download.GetWithContext(ctx, runtime.GOARCH, runtime.GOOS, processor.String(), llamaCPPVersion, dir, tracker); err != nil {
		return fmt.Errorf("llamax: download llama.cpp: %w", err)
	}

	if progress != nil {
		progress("extracting", 0.95)
	}

	// yzma extracts directly into dir. Some archives nest binaries in
	// `build/bin` or `bin`; flatten so platformLibName(dir, …) finds them.
	if err := flattenLibs(dir); err != nil {
		return fmt.Errorf("llamax: flatten: %w", err)
	}
	if !bundlePresent(dir) {
		return errors.New("llamax: required llama.cpp bundle (libs + llama-server) missing after extraction")
	}

	if progress != nil {
		progress("ready", 1.0)
	}
	slog.Info("llamax: install ok", "dir", dir)
	return nil
}

// pickProcessor selects the best-matching llama.cpp build flavor for the host.
//   - darwin/arm64 → Metal
//   - darwin/amd64 → CPU (Metal builds are ARM64-only)
//   - linux + nvidia-smi → CUDA, + rocminfo → ROCm, otherwise → Vulkan
//   - windows + nvidia-smi → CUDA, otherwise → CPU
//
// Users can override by manually placing libs in NURVIS_LIB
func pickProcessor() (download.Processor, error) {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH == "arm64" {
			return download.Metal, nil
		}
		return download.CPU, nil
	case "linux":
		if ok, _ := download.HasCUDA(); ok {
			return download.CUDA, nil
		}
		if ok, _ := download.HasROCm(); ok {
			return download.ROCm, nil
		}
		return download.Vulkan, nil
	case "windows":
		if ok, _ := download.HasCUDA(); ok {
			return download.CUDA, nil
		}
		return download.CPU, nil
	default:
		return download.Processor{}, fmt.Errorf("llamax: unsupported os %s", runtime.GOOS)
	}
}

func versionLabel() string {
	if llamaCPPVersion == "" {
		return "latest"
	}
	return llamaCPPVersion
}

// progressTracker bridges yzma's getter.ProgressTracker to our ProgressFunc.
//
// yzma may call TrackProgress once per file (e.g. cudart + main archive on
// Windows CUDA). We map per-file byte progress into the 0..0.9 range; the
// installer reserves 0.9..1.0 for extraction and finalize.
type progressTracker struct {
	fn ProgressFunc
}

func newProgressTracker(fn ProgressFunc) getter.ProgressTracker {
	if fn == nil {
		return download.ProgressTracker
	}
	return &progressTracker{fn: fn}
}

func (p *progressTracker) TrackProgress(src string, currentSize, totalSize int64, stream io.ReadCloser) io.ReadCloser {
	if totalSize <= 0 || currentSize >= totalSize {
		return stream
	}
	return &progressReader{
		fn:        p.fn,
		src:       src,
		current:   currentSize,
		total:     totalSize,
		startTime: time.Now(),
		inner:     stream,
	}
}

type progressReader struct {
	fn        ProgressFunc
	src       string
	current   int64
	total     int64
	reported  int64
	startTime time.Time
	inner     io.ReadCloser
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		r.current += int64(n)
		// Throttle: report at most every 1 MiB.
		if r.current-r.reported >= 1<<20 {
			r.reported = r.current
			pct := float64(r.current) / float64(r.total) * 0.9
			r.fn("downloading", pct)
		}
	}
	return n, err
}

func (r *progressReader) Close() error {
	if r.fn != nil {
		r.fn("downloading", 0.9)
	}
	return r.inner.Close()
}

// flattenLibs walks dir and moves any .so/.dylib/.dll found in subdirectories
// up to dir itself.
func flattenLibs(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Dir(path) == dir {
			return nil
		}
		if !looksLikeLib(info.Name()) {
			return nil
		}
		dst := filepath.Join(dir, info.Name())
		if path == dst {
			return nil
		}
		if _, err := os.Stat(dst); err == nil {
			return nil // already there
		}
		return os.Rename(path, dst)
	})
}

func looksLikeLib(name string) bool {
	base := filepath.Base(name)
	switch runtime.GOOS {
	case "linux", "freebsd":
		return strings.HasSuffix(base, ".so") || strings.Contains(base, ".so.")
	case "darwin":
		return strings.HasSuffix(base, ".dylib")
	case "windows":
		return strings.HasSuffix(base, ".dll")
	}
	return false
}
