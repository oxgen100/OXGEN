package oxgen

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestGitHubReleaseBootstrapRedirectsAndPinnedVersion(t *testing.T) {
	for _, scenario := range []string{"success", "foreign-redirect", "insecure-cdn", "foreign-artifact"} {
		t.Run(scenario, func(t *testing.T) {
			data := []byte("native OXGEN release archive")
			asset := Artifact{URL: "oxgen_0.4.0_linux_amd64.tar.gz", SHA256: digest(data), Size: int64(len(data))}
			if scenario == "foreign-artifact" {
				asset.URL = "https://untrusted.example/app.tar.gz"
			}
			manifest := ReleaseManifest{Schema: 1, Version: "0.4.0", Artifacts: map[string]Artifact{"linux-amd64": asset}}
			original := http.DefaultTransport
			defer func() { http.DefaultTransport = original }()
			http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				status, location, body := 200, "", []byte(nil)
				switch req.URL.String() {
				case "https://github.com/owner/OXGEN/releases/latest/download/manifest.json":
					status, location = 302, "https://github.com/owner/OXGEN/releases/download/v0.4.0/manifest.json"
					if scenario == "foreign-redirect" {
						location = "https://github.com/other/repo/releases/download/v0.4.0/manifest.json"
					}
				case "https://github.com/owner/OXGEN/releases/download/v0.4.0/manifest.json":
					status, location = 302, "https://release-assets.githubusercontent.com/manifest.json?signed=public"
					if scenario == "insecure-cdn" {
						location = "http://release-assets.githubusercontent.com/manifest.json"
					}
				case "https://release-assets.githubusercontent.com/manifest.json?signed=public":
					body = jsonBytes(manifest)
				case "https://github.com/owner/OXGEN/releases/download/v0.4.0/oxgen_0.4.0_linux_amd64.tar.gz":
					status, location = 302, "https://release-assets.githubusercontent.com/native.tar.gz?signed=public"
				case "https://release-assets.githubusercontent.com/native.tar.gz?signed=public":
					body = data
				default:
					t.Fatalf("unexpected download: %s", req.URL)
					return nil, os.ErrNotExist
				}
				header := http.Header{}
				if location != "" {
					header.Set("Location", location)
				}
				return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
			})
			a := testApp(t, "", &fakeRunner{})
			client, artifact, err := a.linuxArtifact(context.Background(), "https://github.com/owner/OXGEN/releases/latest/download/", "amd64")
			if scenario != "success" {
				if err == nil {
					t.Fatal("accepted invalid release", scenario)
				}
				return
			}
			if err != nil || !strings.Contains(artifact.URL, "/download/v0.4.0/") {
				t.Fatal(artifact, err)
			}
			file, err := client.Download(context.Background(), artifact, t.TempDir(), io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			actual, _ := os.ReadFile(file)
			if !bytes.Equal(actual, data) {
				t.Fatal("download did not match pinned release")
			}
		})
	}
}
