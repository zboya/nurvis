//go:build !windows

package llamax

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// setSysProcAttr puts the child into its own process group on Unix so a
// SIGTERM/SIGKILL targets only llama-server (and any of its children) rather
// than propagating to our entire daemon.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// withLibraryPath augments the parent environment so the dynamic linker can
// find the bundled llama.cpp shared libraries shipped alongside `llama-server`.
//
//   - macOS  → DYLD_LIBRARY_PATH
//   - Linux  → LD_LIBRARY_PATH
func withLibraryPath(libDir string) []string {
	envName := "LD_LIBRARY_PATH"
	if runtime.GOOS == "darwin" {
		envName = "DYLD_LIBRARY_PATH"
	}
	abs, err := filepath.Abs(libDir)
	if err != nil {
		abs = libDir
	}
	env := os.Environ()
	for i, kv := range env {
		if strings.HasPrefix(kv, envName+"=") {
			env[i] = envName + "=" + abs + string(os.PathListSeparator) + strings.TrimPrefix(kv, envName+"=")
			return env
		}
	}
	return append(env, envName+"="+abs)
}

// serverBinaryPath returns the absolute path to the `llama-server` executable
// inside libDir.
func serverBinaryPath(libDir string) string {
	return filepath.Join(libDir, "llama-server")
}
