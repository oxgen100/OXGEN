package oxgen

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

type Client struct {
	HTTP *http.Client
	// Tests inject an in-process HTTP transport; production never enables this.
	allowTest bool
}

func newClient() *Client {
	c := &Client{}
	c.HTTP = &http.Client{Timeout: 30 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 6 {
			return fmt.Errorf("重定向过多")
		}
		return officialURL(req.URL.String())
	}}
	return c
}

func officialURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme != "https" || u.User != nil || u.Port() != "" && u.Port() != "443" {
		return fmt.Errorf("仅允许官方 HTTPS 地址: %s", raw)
	}
	switch strings.ToLower(u.Hostname()) {
	case "nodejs.org", "www.python.org", "python.org", "downloads.python.org", "api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "www.kernel.org", "kernel.org":
		return nil
	case "update.code.visualstudio.com", "vscode.download.prss.microsoft.com":
		return nil
	case "registry.npmjs.org", "releases.openai.com", "downloads.claude.ai", "www.codebuddy.cn", "download.codebuddy.cn", "acc-1258344699.cos.ap-guangzhou.myqcloud.com":
		return nil
	}
	return fmt.Errorf("不是支持的官方来源: %s", u.Hostname())
}

func (c *Client) request(ctx context.Context, raw string) (*http.Response, error) {
	if !c.allowTest {
		if err := officialURL(raw); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "OXGEN/0.1 (+https://go.dev)")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("官方来源返回 HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) Text(ctx context.Context, raw string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	resp, err := c.request(ctx, raw)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 20*1024*1024+1))
	if err != nil {
		return "", err
	}
	if len(b) > 20*1024*1024 {
		return "", fmt.Errorf("官方元数据超出大小限制")
	}
	return string(b), nil
}

func (c *Client) JSON(ctx context.Context, raw string, value any) error {
	s, err := c.Text(ctx, raw)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(s), value)
}

func validSHA(s string) bool    { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func validSHA512(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 64 }

func fileSHA(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileSHA512(file string) (string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha512.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type progressWriter struct {
	out   io.Writer
	last  time.Time
	n     int64
	total int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if time.Since(p.last) > 2*time.Second {
		fmt.Fprintf(p.out, "\r已下载 %.1f MiB", float64(p.n)/1048576)
		if p.total > 0 {
			fmt.Fprintf(p.out, " / %.1f MiB", float64(p.total)/1048576)
		}
		p.last = time.Now()
	}
	return len(b), nil
}

func (c *Client) Download(ctx context.Context, a Artifact, dir string, out io.Writer) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !c.allowTest {
		if err := officialURL(a.URL); err != nil {
			return "", err
		}
	}
	if err := ensureWritable(dir); err != nil {
		return "", err
	}
	u, err := url.Parse(a.URL)
	if err != nil {
		return "", err
	}
	name := path.Base(u.Path)
	if name == "." || name == "/" || strings.ContainsAny(name, "\\:") {
		return "", fmt.Errorf("无效安装包名称")
	}
	if a.SHA256 != "" && !validSHA(a.SHA256) {
		return "", fmt.Errorf("官方校验值不是有效 SHA256")
	}
	if a.SHA512 != "" && !validSHA512(a.SHA512) {
		return "", fmt.Errorf("官方 SHA512 校验值无效")
	}
	if a.SHA256 == "" && a.SHA512 == "" && a.Signature == "" {
		return "", fmt.Errorf("安装包缺少校验值和可验证的发布者签名")
	}
	dest := filepath.Join(dir, name)
	verified := a.SHA256 != "" || a.SHA512 != ""
	if a.SHA256 != "" {
		sum, e := fileSHA(dest)
		verified = verified && e == nil && strings.EqualFold(sum, a.SHA256)
	}
	if a.SHA512 != "" {
		sum, e := fileSHA512(dest)
		verified = verified && e == nil && strings.EqualFold(sum, a.SHA512)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if verified {
		fmt.Fprintln(out, "复用已通过官方 SHA256/SHA512 校验的发行包。")
		return dest, nil
	}
	if a.SHA256 == "" && a.SHA512 == "" && a.Signature != "" && strings.HasSuffix(name, ".exe") {
		if info, e := os.Stat(dest); e == nil && info.Mode().IsRegular() {
			fmt.Fprintln(out, "复用已下载的安装器；执行前仍须重新验证发布者签名。")
			return dest, nil
		}
	}
	resp, err := c.request(ctx, a.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	need := resp.ContentLength*3 + 128*1024*1024
	if err = checkSpace(dir, need); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".oxgen-download-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	h := sha256.New()
	h512 := sha512.New()
	p := &progressWriter{out: out, total: resp.ContentLength}
	n, copyErr := io.Copy(io.MultiWriter(f, h, h512, p), io.LimitReader(resp.Body, 4*1024*1024*1024+1))
	closeErr := f.Close()
	fmt.Fprintln(out)
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > 4*1024*1024*1024 {
		return "", fmt.Errorf("下载超出 4 GiB 限制")
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return "", fmt.Errorf("下载不完整")
	}
	if a.Size > 0 && n != a.Size {
		return "", fmt.Errorf("下载大小与官方元数据不一致")
	}
	if a.SHA256 != "" && !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), a.SHA256) {
		return "", fmt.Errorf("SHA256 校验失败，安装未执行")
	}
	if a.SHA512 != "" && !strings.EqualFold(hex.EncodeToString(h512.Sum(nil)), a.SHA512) {
		return "", fmt.Errorf("SHA512 校验失败，安装未执行")
	}
	if _, err = os.Stat(dest); err == nil {
		if err = os.Rename(dest, dest+fmt.Sprintf(".previous-%d", time.Now().UnixNano())); err != nil {
			return "", err
		}
	}
	if err = os.Rename(tmp, dest); err != nil {
		return "", err
	}
	fmt.Fprintln(out, "下载完成；后续执行前将检查所需签名。")
	return dest, nil
}
