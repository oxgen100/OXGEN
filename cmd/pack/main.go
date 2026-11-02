// Command pack creates a static server directory and all six native releases.
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "0.4.0", "release version")
	out := flag.String("out", "dist/server", "static server directory")
	base := flag.String("base-url", "", "optional HTTPS download base; may be configured later")
	github := flag.String("github-repository", "", "GitHub owner/repo; publish flat release assets")
	flag.Parse()
	if *github != "" {
		if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(*github) || *base != "" {
			return fmt.Errorf("provide a valid owner/repo, without -base-url")
		}
		*base = "https://github.com/" + *github + "/releases/latest/download"
	}
	if !regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[A-Za-z0-9.-]+)?$`).MatchString(*version) {
		return fmt.Errorf("invalid release version")
	}
	if *base != "" {
		u, e := url.Parse(*base)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("base-url must be an HTTPS URL without credentials/query")
		}
	}
	root, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	releaseDir := filepath.Join(root, "releases", *version)
	if err = os.MkdirAll(releaseDir, 0755); err != nil {
		return err
	}
	manifest := struct {
		Schema    int                 `json:"schema"`
		Version   string              `json:"version"`
		Artifacts map[string]artifact `json:"artifacts"`
	}{1, *version, map[string]artifact{}}
	tsv := "# oxgen-manifest-v1\t" + *version + "\n"
	for _, platform := range []string{"windows", "darwin", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			key := platform + "-" + arch
			stage := filepath.Join(root, "build", key)
			if err = os.MkdirAll(filepath.Join(stage, "bin"), 0755); err != nil {
				return err
			}
			name := "oxgen"
			if platform == "windows" {
				name += ".exe"
			}
			binary := filepath.Join(stage, "bin", name)
			command := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w -X main.version="+*version, "-o", binary, "./cmd/oxgen")
			command.Env = append(os.Environ(), "GOOS="+platform, "GOARCH="+arch, "CGO_ENABLED=0")
			command.Stdout = os.Stdout
			command.Stderr = os.Stderr
			fmt.Println("Building", key)
			if err = command.Run(); err != nil {
				return err
			}
			readme, err := os.ReadFile("README.md")
			if err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(stage, "README.md"), readme, 0644); err != nil {
				return err
			}
			validation, err := os.ReadFile(filepath.Join("docs", "VALIDATION.md"))
			if err != nil {
				return err
			}
			if err = os.MkdirAll(filepath.Join(stage, "docs"), 0755); err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(stage, "docs", "VALIDATION.md"), validation, 0644); err != nil {
				return err
			}
			for _, name := range []string{"AGENT-INSTALL.md", "TERMINAL-DESIGN.md"} {
				b, e := os.ReadFile(filepath.Join("docs", name))
				if e != nil {
					return e
				}
				if e = os.WriteFile(filepath.Join(stage, "docs", name), b, 0644); e != nil {
					return e
				}
			}
			if err = os.WriteFile(filepath.Join(stage, "server-url.txt"), []byte(strings.TrimRight(*base, "/")+"\n"), 0644); err != nil {
				return err
			}
			ext := ".tar.gz"
			if platform == "windows" {
				ext = ".zip"
			}
			archiveName := "oxgen_" + *version + "_" + platform + "_" + arch + ext
			archive := filepath.Join(releaseDir, archiveName)
			if err = makeArchive(archive, stage, platform == "windows"); err != nil {
				return err
			}
			f, err := os.Open(archive)
			if err != nil {
				return err
			}
			h := sha256.New()
			size, err := io.Copy(h, f)
			f.Close()
			if err != nil {
				return err
			}
			entry := artifact{URL: "releases/" + *version + "/" + archiveName, SHA256: hex.EncodeToString(h.Sum(nil)), Size: size}
			if *github != "" {
				entry.URL = archiveName
			}
			manifest.Artifacts[key] = entry
			tsv += fmt.Sprintf("%s\t%s\t%s\t%d\t%s\n", key, entry.URL, entry.SHA256, entry.Size, *version)
		}
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.json"), append(b, '\n'), 0644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(root, "manifest.tsv"), []byte(tsv), 0644); err != nil {
		return err
	}
	for _, name := range []string{"install.ps1", "install.sh"} {
		content, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			return err
		}
		text := strings.ReplaceAll(string(content), "@OXGEN_BASE_URL@", strings.TrimRight(*base, "/"))
		// A UTF-8 BOM is required for Chinese text in Windows PowerShell 5.1.
		body := []byte(text)
		if strings.HasSuffix(name, ".ps1") {
			body = append([]byte{0xef, 0xbb, 0xbf}, body...)
		}
		if err = os.WriteFile(filepath.Join(root, name), body, 0755); err != nil {
			return err
		}
	}
	if err = os.WriteFile(filepath.Join(root, "README.txt"), []byte("Upload install.ps1, install.sh, manifest.json, manifest.tsv and releases/ to your HTTPS /oxgen/ directory.\nDo not upload build/; it is for local validation only.\nNo third-party dependency installers are hosted here.\n"), 0644); err != nil {
		return err
	}
	fmt.Println("Server files ready:", root)
	if *base == "" {
		fmt.Println("Domain pending. Pass -BaseUrl / --base-url to the bootstrap, or rebuild with -base-url when ready.")
	}
	return nil
}

func makeArchive(file, root string, windows bool) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	defer f.Close()
	if windows {
		w := zip.NewWriter(f)
		err = filepath.WalkDir(root, func(name string, entry os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if entry.IsDir() {
				return nil
			}
			relative, _ := filepath.Rel(root, name)
			header := &zip.FileHeader{Name: filepath.ToSlash(relative), Method: zip.Deflate}
			header.SetMode(0755)
			part, e := w.CreateHeader(header)
			if e != nil {
				return e
			}
			in, e := os.Open(name)
			if e != nil {
				return e
			}
			defer in.Close()
			_, e = io.Copy(part, in)
			return e
		})
		if err != nil {
			w.Close()
			return err
		}
		return w.Close()
	}
	gz := gzip.NewWriter(f)
	w := tar.NewWriter(gz)
	err = filepath.WalkDir(root, func(name string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(root, name)
		stat, e := entry.Info()
		if e != nil {
			return e
		}
		header := &tar.Header{Name: filepath.ToSlash(relative), Size: stat.Size(), Mode: 0755}
		if e = w.WriteHeader(header); e != nil {
			return e
		}
		in, e := os.Open(name)
		if e != nil {
			return e
		}
		defer in.Close()
		_, e = io.Copy(w, in)
		return e
	})
	if err != nil {
		w.Close()
		gz.Close()
		return err
	}
	if err = w.Close(); err != nil {
		gz.Close()
		return err
	}
	return gz.Close()
}
