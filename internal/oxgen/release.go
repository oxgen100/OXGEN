package oxgen

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

type ReleaseManifest struct {
	Schema    int                 `json:"schema"`
	Version   string              `json:"version"`
	Artifacts map[string]Artifact `json:"artifacts"`
}

func releaseClient(base string) (*Client, *url.URL, error) {
	u, err := url.Parse(base)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, nil, fmt.Errorf("下载根地址需要无账号、查询参数的 HTTPS 地址")
	}
	u.Path = path.Clean("/" + strings.TrimPrefix(u.Path, "/"))
	if u.Path != "/" {
		u.Path += "/"
	}
	u.RawPath = ""
	githubRoot := ""
	if u.Host == "github.com" {
		match := regexp.MustCompile(`^/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/releases/(?:latest/download|download/v[0-9A-Za-z.-]+)/$`).FindStringSubmatch(u.Path)
		if match == nil {
			return nil, nil, fmt.Errorf("GitHub 下载地址需要指向仓库的 Release")
		}
		githubRoot = "/" + match[1] + "/releases/"
	}
	check := func(raw string) error {
		p, e := url.Parse(raw)
		if e != nil {
			return e
		}
		if githubRoot != "" {
			if p.Scheme != "https" || p.User != nil || p.Port() != "" && p.Port() != "443" || p.Fragment != "" {
				return fmt.Errorf("GitHub 发行地址无效")
			}
			if p.Hostname() == "release-assets.githubusercontent.com" {
				return nil
			}
			if p.Host == "github.com" && strings.HasPrefix(path.Clean(p.Path), githubRoot) && p.RawQuery == "" {
				return nil
			}
			return fmt.Errorf("下载地址越出 GitHub 仓库和官方发行 CDN")
		}
		if p.Scheme != u.Scheme || p.Host != u.Host || !strings.HasPrefix(path.Clean(p.Path), u.Path) || p.User != nil {
			return fmt.Errorf("OXGEN 下载地址越出已配置服务器目录")
		}
		return nil
	}
	c := &Client{allowTest: true}
	c.HTTP = &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("重定向过多")
		}
		return check(req.URL.String())
	}, Transport: &originTransport{base: http.DefaultTransport, check: check}}
	return c, u, nil
}

type originTransport struct {
	base  http.RoundTripper
	check func(string) error
}

func (t *originTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.check(r.URL.String()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(r)
}

func (a *App) linuxArtifact(ctx context.Context, base, arch string) (*Client, Artifact, error) {
	c, u, err := releaseClient(base)
	if err != nil {
		return nil, Artifact{}, err
	}
	var manifest ReleaseManifest
	if err = c.JSON(ctx, u.ResolveReference(&url.URL{Path: "manifest.json"}).String(), &manifest); err != nil {
		return nil, Artifact{}, err
	}
	if manifest.Schema != 1 {
		return nil, Artifact{}, fmt.Errorf("不支持服务器发布清单版本")
	}
	artifact, ok := manifest.Artifacts["linux-"+arch]
	if !ok || !validSHA(artifact.SHA256) {
		return nil, Artifact{}, fmt.Errorf("清单缺少该架构的 Linux 程序或 SHA256")
	}
	if u.Host == "github.com" {
		if !regexp.MustCompile(`^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$`).MatchString(manifest.Version) {
			return nil, Artifact{}, fmt.Errorf("GitHub 清单版本无效")
		}
		root, _, _ := strings.Cut(u.Path, "/releases/")
		u.Path = root + "/releases/download/v" + manifest.Version + "/"
	}
	rel, err := url.Parse(artifact.URL)
	if err != nil {
		return nil, Artifact{}, err
	}
	artifact.URL = u.ResolveReference(rel).String()
	resolved, _ := url.Parse(artifact.URL)
	if resolved.Scheme != u.Scheme || resolved.Host != u.Host || !strings.HasPrefix(path.Clean(resolved.Path), u.Path) || resolved.User != nil || resolved.RawQuery != "" || resolved.Fragment != "" {
		return nil, Artifact{}, fmt.Errorf("清单安装包越出指定发布目录")
	}
	return c, artifact, nil
}
