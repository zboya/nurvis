//go:build windows

package llamax

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// setSysProcAttr is a no-op on Windows; we rely on Process.Kill at shutdown.
func setSysProcAttr(cmd *exec.Cmd) {}

// withLibraryPath prepends libDir to PATH so Windows resolves the bundled
// .dll files when launching llama-server.exe.
func withLibraryPath(libDir string) []string {
	abs, err := filepath.Abs(libDir)
	if err != nil {
		abs = libDir
	}
	env := os.Environ()
	for i, kv := range env {
		if strings.HasPrefix(strings.ToUpper(kv), "PATH=") {
			rest := kv[len("PATH="):]
			env[i] = "PATH=" + abs + string(os.PathListSeparator) + rest
			return env
		}
	}
	return append(env, "PATH="+abs)
}

// serverBinaryPath returns the absolute path to llama-server.exe in libDir.
func serverBinaryPath(libDir string) string {
	return filepath.Join(libDir, "llama-server.exe")
}
