package oxgen

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
)

func detectHost() Host {
	h := Host{OS: runtime.GOOS, Arch: runtime.GOARCH}
	if h.OS == "linux" {
		if b, err := os.ReadFile("/etc/os-release"); err == nil {
			m := parseOSRelease(string(b))
			h.Distro = m["ID"]
			h.Release = m["VERSION_ID"]
			h.Codename = m["VERSION_CODENAME"]
			if h.Codename == "" {
				h.Codename = m["UBUNTU_CODENAME"]
			}
		}
	}
	return h
}

func parseOSRelease(s string) map[string]string {
	m := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(s))
	for scanner.Scan() {
		k, v, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			m[k] = strings.Trim(v, "\"'")
		}
	}
	return m
}

func (h Host) Supported() error {
	if h.Arch != "amd64" && h.Arch != "arm64" {
		return fmt.Errorf("暂不支持架构 %s；首版支持 x64、ARM64", h.Arch)
	}
	switch h.OS {
	case "windows", "darwin":
		return nil
	case "linux":
		if h.Distro == "ubuntu" || h.Distro == "debian" {
			return nil
		}
	}
	return fmt.Errorf("暂不支持 %s %s；Linux 首版支持 Ubuntu／Debian", h.OS, h.Distro)
}

func decodeOutput(b []byte) string {
	// wsl.exe emits UTF-16LE even when stdout is redirected.
	nulls := 0
	for _, v := range b {
		if v == 0 {
			nulls++
		}
	}
	if len(b) > 1 && (b[0] == 0xff && b[1] == 0xfe || b[1] == 0 || nulls > len(b)/10) {
		if b[0] == 0xff && b[1] == 0xfe {
			b = b[2:]
		}
		u := make([]uint16, len(b)/2)
		for i := range u {
			u[i] = binary.LittleEndian.Uint16(b[i*2:])
		}
		return string(utf16.Decode(u))
	}
	return string(b)
}

func defaultHome() string {
	if value := os.Getenv("OXGEN_HOME"); value != "" {
		return value
	}
	if exe, err := os.Executable(); err == nil && filepath.Base(filepath.Dir(exe)) == "bin" {
		return filepath.Dir(filepath.Dir(exe))
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".oxgen")
}

func ensureWritable(dir string) error {
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("请输入完整绝对路径")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".oxgen-write-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func contained(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func removeTemp(root, path string) error {
	root, _ = filepath.Abs(root)
	path, _ = filepath.Abs(path)
	if !contained(root, path) {
		return fmt.Errorf("拒绝清理工作目录之外的路径: %s", path)
	}
	return os.RemoveAll(path)
}
