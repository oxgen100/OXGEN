package oxgen

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCancelledPromptReturnsWithoutInput(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	u := newUI(r, io.Discard, false)
	u.Context = ctx
	done := make(chan error, 1)
	go func() { _, err := u.Ask("prompt", ""); done <- err }()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled prompt remained blocked")
	}
}

// Black-box bootstrap test with a local file transport. Only the download
// command and user confirmation are stubbed; checksum, extraction, path guards,
// native executable check and replacement run as shipped. No user PATH changes.
func TestWindowsBootstrapWithLocalRelease(t *testing.T) {
	serverRoot := os.Getenv("OXGEN_BOOTSTRAP_FIXTURE")
	if serverRoot == "" || runtime.GOOS != "windows" {
		t.Skip("set OXGEN_BOOTSTRAP_FIXTURE to a generated server directory")
	}
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(serverRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ReleaseManifest
	if err = json.Unmarshal(b, &manifest); err != nil {
		t.Fatal(err)
	}
	asset, ok := manifest.Artifacts["windows-"+runtime.GOARCH]
	if !ok {
		t.Fatal("missing native build")
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"success", "bad-checksum", "foreign-artifact"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			fixture := filepath.Join(root, "server")
			os.MkdirAll(fixture, 0755)
			one := ReleaseManifest{Schema: 1, Version: manifest.Version, Artifacts: map[string]Artifact{"windows-" + runtime.GOARCH: asset}}
			if scenario == "bad-checksum" {
				changed := asset
				changed.SHA256 = strings.Repeat("0", 64)
				one.Artifacts["windows-"+runtime.GOARCH] = changed
			}
			if scenario == "foreign-artifact" {
				changed := asset
				changed.URL = "https://unapproved.invalid/package.zip"
				one.Artifacts["windows-"+runtime.GOARCH] = changed
			}
			if err = saveJSON(filepath.Join(fixture, "manifest.json"), one); err != nil {
				t.Fatal(err)
			}
			artifactDest := filepath.Join(fixture, filepath.FromSlash(asset.URL))
			os.MkdirAll(filepath.Dir(artifactDest), 0755)
			data, err := os.ReadFile(filepath.Join(serverRoot, filepath.FromSlash(asset.URL)))
			if os.IsNotExist(err) {
				data, err = os.ReadFile(filepath.Join(serverRoot, "releases", manifest.Version, filepath.Base(asset.URL)))
			}
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(artifactDest, data, 0600)
			bootstrap := filepath.Join(root, "install.ps1")
			os.WriteFile(bootstrap, append([]byte{0xef, 0xbb, 0xbf}, source...), 0600)
			home := filepath.Join(root, "安装 中文 space")
			cache := filepath.Join(root, "下载 中文 space")
			os.MkdirAll(filepath.Join(home, "bin"), 0755)
			dest := filepath.Join(home, "bin", "oxgen.exe")
			os.WriteFile(dest, []byte("preserve old program"), 0600)
			wrapper := filepath.Join(root, "fixture.ps1")
			script := `param([string]$Fixture,[string]$Bootstrap,[string]$Target,[string]$Cache)
$ErrorActionPreference='Stop'
function Invoke-WebRequest {
 param([Uri]$Uri,[string]$OutFile,[switch]$UseBasicParsing,[int]$MaximumRedirection,[int]$TimeoutSec)
 if ($Uri.Scheme -ne 'https' -or $Uri.Host -ne 'fixture.invalid' -or -not $Uri.AbsolutePath.StartsWith('/oxgen/')) { throw 'unexpected transport target' }
 $relative=$Uri.AbsolutePath.Substring('/oxgen/'.Length).Replace('/',[IO.Path]::DirectorySeparatorChar)
 Copy-Item -LiteralPath (Join-Path $Fixture $relative) -Destination $OutFile
}
function Read-Host { param([string]$Prompt) return '1' }
& $Bootstrap -BaseUrl 'https://fixture.invalid/oxgen/' -InstallDir $Target -DownloadDir $Cache -NoPath -NoLaunch
`
			os.WriteFile(wrapper, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", wrapper, fixture, bootstrap, home, cache)
			output, runErr := cmd.CombinedOutput()
			if scenario == "success" {
				if runErr != nil {
					t.Fatalf("bootstrap failed: %v\n%s", runErr, output)
				}
				check, err := exec.Command(dest, "--version").CombinedOutput()
				if err != nil || !bytes.Contains(check, []byte(manifest.Version)) {
					t.Fatal(err, string(check))
				}
				if b, _ := os.ReadFile(filepath.Join(home, "server-url.txt")); string(b) != "https://fixture.invalid/oxgen/" {
					t.Fatal("server URL not saved")
				}
			} else {
				if runErr == nil {
					t.Fatal("bootstrap accepted invalid artifact", string(output))
				}
				if b, _ := os.ReadFile(dest); string(b) != "preserve old program" {
					t.Fatal("failed bootstrap replaced old executable")
				}
			}
			entries, _ := os.ReadDir(cache)
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".oxgen-bootstrap-") {
					t.Fatal("bootstrap did not clean owned temporary directory")
				}
			}
		})
	}
}
