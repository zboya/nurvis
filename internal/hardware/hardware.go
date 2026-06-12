// Package hardware probes the local CPU/RAM/GPU information and recommends
// suitable HuggingFace GGUF model tiers based on it.
package hardware

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// GPU 描述一块 GPU 设备。
type GPU struct {
	Vendor string  `json:"vendor"` // apple | nvidia | amd
	Name   string  `json:"name"`
	VRAMGB float64 `json:"vram_gb"` // 显存（GB），Apple Silicon 使用统一内存时为 0
}

// Info 包含本机硬件概况。
type Info struct {
	TotalRAMBytes  uint64  `json:"total_ram_bytes"`
	RAMGB          float64 `json:"ram_gb"`
	CPUCores       int     `json:"cpu_cores"` // logical CPU count
	GPUs           []GPU   `json:"gpus"`
	Platform       string  `json:"platform"` // darwin/linux/windows
	Arch           string  `json:"arch"`     // arm64/amd64
	IsAppleSilicon bool    `json:"is_apple_silicon"`
}

// Probe 探测当前机器硬件信息。
func Probe() (Info, error) {
	info := Info{
		Platform: runtime.GOOS,
		Arch:     runtime.GOARCH,
	}
	info.IsAppleSilicon = runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
	info.CPUCores = runtime.NumCPU()

	if err := probeRAM(&info); err != nil {
		return info, fmt.Errorf("hardware: probe RAM: %w", err)
	}
	probeGPU(&info) // GPU 探测失败不阻塞启动
	return info, nil
}

// Recommend returns a list of recommended GGUF model identifiers
// ("<repo>/<file>") in priority order based on the available memory.
//
// We reserve 4 GB for the operating system and use the larger of total RAM and
// dedicated VRAM as the budget. The returned strings are accepted directly by
// modelmgr.ParseRef and the gateway models.pull RPC.
//
// All entries below are verified against the actual files published under the
// ggml-org HuggingFace org (Gemma-4 family + Qwen3.6 family). Note that
// Qwen3.6 GGUFs from ggml-org only ship Q8_0 / BF16 (no Q4_K_M), and Gemma-4
// E2B only ships Q8_0; pick quants accordingly per tier.
func Recommend(hw Info) []string {
	effectiveGB := hw.RAMGB
	for _, g := range hw.GPUs {
		if g.VRAMGB > effectiveGB {
			effectiveGB = g.VRAMGB
		}
	}
	const systemReserveGB = 8
	available := effectiveGB - systemReserveGB

	switch {
	case available >= 48:
		// Workstation / high-end GPU (>= ~48 GB usable)
		return []string{
			"ggml-org/gemma-4-31B-it-GGUF/gemma-4-31B-it-Q4_K_M.gguf",
			"ggml-org/Qwen3.6-35B-A3B-GGUF/Qwen3.6-35B-A3B-Q8_0.gguf",
			"ggml-org/Qwen3.6-27B-GGUF/Qwen3.6-27B-Q8_0.gguf",
			"ggml-org/gemma-4-26B-A4B-it-GGUF/gemma-4-26B-A4B-it-Q4_K_M.gguf",
			"ggml-org/gemma-4-12B-it-GGUF/gemma-4-12B-it-Q4_K_M.gguf",
			"ggml-org/gemma-4-E4B-it-GGUF/gemma-4-E4B-it-Q8_0.gguf",
		}
	case available >= 32:
		// 32 GB tier
		return []string{
			"ggml-org/gemma-4-26B-A4B-it-GGUF/gemma-4-26B-A4B-it-Q4_K_M.gguf",
			"ggml-org/Qwen3.6-27B-GGUF/Qwen3.6-27B-Q8_0.gguf",
			"ggml-org/gemma-4-12B-it-GGUF/gemma-4-12B-it-Q4_K_M.gguf",
			"ggml-org/gemma-4-E4B-it-GGUF/gemma-4-E4B-it-Q4_K_M.gguf",
		}
	case available >= 16:
		// 16 GB tier
		return []string{
			"ggml-org/gemma-4-12B-it-GGUF/gemma-4-12B-it-Q4_K_M.gguf",
			"ggml-org/gemma-4-E4B-it-GGUF/gemma-4-E4B-it-Q4_K_M.gguf",
			"ggml-org/gemma-4-E4B-it-GGUF/gemma-4-E4B-it-Q8_0.gguf",
			"ggml-org/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-Q8_0.gguf",
		}
	case available >= 8:
		// 8 GB tier
		return []string{
			"ggml-org/gemma-4-E4B-it-GGUF/gemma-4-E4B-it-Q4_K_M.gguf",
			"ggml-org/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-Q8_0.gguf",
		}
	default:
		// Low-memory fallback
		return []string{
			"ggml-org/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-Q8_0.gguf",
		}
	}
}

// DefaultModel returns the recommended top model identifier for this hardware.
func DefaultModel(hw Info) string {
	r := Recommend(hw)
	if len(r) == 0 {
		return "ggml-org/gemma-4-E2B-it-GGUF/gemma-4-E2B-it-Q8_0.gguf"
	}
	return r[0]
}

// --- platform-specific RAM probe ---

func probeRAM(info *Info) error {
	switch runtime.GOOS {
	case "darwin":
		return probeRAMDarwin(info)
	case "linux":
		return probeRAMLinux(info)
	case "windows":
		return probeRAMWindows(info)
	default:
		// Other platforms: fall back to 8 GB to avoid startup failure
		info.TotalRAMBytes = 8 << 30
		info.RAMGB = 8
		return nil
	}
}

func probeRAMDarwin(info *Info) error {
	out, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return err
	}
	bytes, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return err
	}
	info.TotalRAMBytes = bytes
	info.RAMGB = float64(bytes) / (1 << 30)
	return nil
}

func probeRAMLinux(info *Info) error {
	out, err := exec.Command("grep", "MemTotal", "/proc/meminfo").Output()
	if err != nil {
		return err
	}
	// "MemTotal:       16384000 kB"
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return fmt.Errorf("unexpected /proc/meminfo format")
	}
	kb, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return err
	}
	info.TotalRAMBytes = kb * 1024
	info.RAMGB = float64(kb) / (1 << 20)
	return nil
}

func probeRAMWindows(info *Info) error {
	// wmic OS get TotalVisibleMemorySize /Value  →  "TotalVisibleMemorySize=16777216\r\n"
	out, err := exec.Command("wmic", "OS", "get", "TotalVisibleMemorySize", "/Value").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "TotalVisibleMemorySize=") {
			continue
		}
		val := strings.TrimPrefix(line, "TotalVisibleMemorySize=")
		val = strings.TrimSpace(val)
		kb, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return fmt.Errorf("parse TotalVisibleMemorySize: %w", err)
		}
		info.TotalRAMBytes = kb * 1024
		info.RAMGB = float64(kb) / (1 << 20)
		return nil
	}
	return fmt.Errorf("TotalVisibleMemorySize not found in wmic output")
}

func probeGPU(info *Info) {
	if info.IsAppleSilicon {
		// Apple Silicon uses unified memory; no separate GPU VRAM
		info.GPUs = []GPU{{Vendor: "apple", Name: "Apple Silicon"}}
		return
	}
	// Linux/Windows: try nvidia-smi first (most accurate VRAM info)
	probeNvidiaGPU(info)
	// Windows fallback: use wmic to detect AMD / Intel / other GPUs
	if runtime.GOOS == "windows" && len(info.GPUs) == 0 {
		probeWmicGPU(info)
	}
}

func probeNvidiaGPU(info *Info) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.total",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return // 没有 nvidia GPU，忽略
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		mb, _ := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		info.GPUs = append(info.GPUs, GPU{
			Vendor: "nvidia",
			Name:   strings.TrimSpace(parts[0]),
			VRAMGB: mb / 1024,
		})
	}
}

// probeWmicGPU uses wmic to detect GPU name and VRAM on Windows.
// AdapterRAM is reported in bytes; some drivers report 0 or an unreliable value
// for discrete GPUs, so we treat 0 as unknown rather than an error.
func probeWmicGPU(info *Info) {
	out, err := exec.Command("wmic", "path", "win32_VideoController",
		"get", "Name,AdapterRAM", "/format:csv").Output()
	if err != nil {
		return
	}
	for i, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(strings.ReplaceAll(line, "\r", ""))
		if i == 0 || line == "" {
			// skip CSV header and blank lines
			continue
		}
		// CSV columns: Node,AdapterRAM,Name
		parts := strings.SplitN(line, ",", 3)
		if len(parts) < 3 {
			continue
		}
		adapterRAM := strings.TrimSpace(parts[1])
		name := strings.TrimSpace(parts[2])
		if name == "" {
			continue
		}
		var vramGB float64
		if b, err := strconv.ParseUint(adapterRAM, 10, 64); err == nil && b > 0 {
			vramGB = float64(b) / (1 << 30)
		}
		vendor := "unknown"
		nameLower := strings.ToLower(name)
		switch {
		case strings.Contains(nameLower, "nvidia"):
			vendor = "nvidia"
		case strings.Contains(nameLower, "amd") || strings.Contains(nameLower, "radeon"):
			vendor = "amd"
		case strings.Contains(nameLower, "intel"):
			vendor = "intel"
		}
		info.GPUs = append(info.GPUs, GPU{
			Vendor: vendor,
			Name:   name,
			VRAMGB: vramGB,
		})
	}
}
