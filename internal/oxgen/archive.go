package oxgen

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func archivePath(root, name string, strip bool, links map[string]bool) (string, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return "", fmt.Errorf("安装包包含绝对路径")
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return "", fmt.Errorf("安装包包含越界路径")
		}
	}
	if strip {
		_, name, _ = strings.Cut(name, "/")
	}
	if name == "" || name == "." {
		return "", nil
	}
	dest := filepath.Join(root, filepath.FromSlash(name))
	if !contained(root, dest) {
		return "", fmt.Errorf("安装包路径越界")
	}
	// Do not follow archive-created links when writing later entries.
	for p := filepath.Dir(dest); p != root && contained(root, p); p = filepath.Dir(p) {
		if links[p] {
			return "", fmt.Errorf("安装包尝试通过符号链接写入")
		}
	}
	return dest, nil
}

func extractArchive(file, dest string, strip bool) error {
	return extractArchiveContext(context.Background(), file, dest, strip)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(b)
}

func extractArchiveContext(ctx context.Context, file, dest string, strip bool) error {
	return extractArchiveOptions(ctx, file, dest, strip, false)
}

func extractArchiveOptions(ctx context.Context, file, dest string, strip, allowZIPLinks bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("目标目录已存在，为保留现有文件未覆盖: %s", dest)
	} else if !os.IsNotExist(err) {
		return err
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(parent, ".oxgen-stage-*")
	if err != nil {
		return err
	}
	defer removeTemp(parent, stage)
	// Stage is a new private extraction tree. Track the directories and symlinks
	// created by this archive rather than stat'ing every ancestor for every file.
	// This also prevents following archive-created links without costly cross-VM IO.
	links := map[string]bool{}
	directories := map[string]bool{stage: true}
	mkdir := func(p string) error {
		if directories[p] {
			return nil
		}
		if err := os.MkdirAll(p, 0755); err != nil {
			return err
		}
		for p != stage && contained(stage, p) {
			directories[p] = true
			p = filepath.Dir(p)
		}
		return nil
	}
	var expanded int64
	entries := 0
	write := func(name string, mode os.FileMode, size int64, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries++
		if entries > 200000 {
			return fmt.Errorf("安装包文件数量超出限制")
		}
		p, e := archivePath(stage, name, strip, links)
		if e != nil || p == "" {
			return e
		}
		if size < 0 || size > 8*1024*1024*1024-expanded {
			return fmt.Errorf("解压大小超出限制")
		}
		expanded += size
		if mode.IsDir() {
			return mkdir(p)
		}
		if mode&os.ModeSymlink != 0 {
			return fmt.Errorf("ZIP 安装包不允许符号链接")
		}
		if e = mkdir(filepath.Dir(p)); e != nil {
			return e
		}
		f, e := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm()&0755|0600)
		if e != nil {
			return e
		}
		_, e = io.Copy(f, io.LimitReader(contextReader{ctx, r}, size))
		ce := f.Close()
		if e != nil {
			return e
		}
		return ce
	}
	if strings.HasSuffix(strings.ToLower(file), ".zip") {
		z, e := zip.OpenReader(file)
		if e != nil {
			return e
		}
		defer z.Close()
		type zipLink struct{ path, target string }
		var pending []zipLink
		for _, entry := range z.File {
			if err := ctx.Err(); err != nil {
				return err
			}
			r, e := entry.Open()
			if e != nil {
				return e
			}
			if allowZIPLinks && entry.Mode()&os.ModeSymlink != 0 {
				p, pathErr := archivePath(stage, entry.Name, strip, links)
				if pathErr != nil || p == "" {
					r.Close()
					return fmt.Errorf("应用 ZIP 的符号链接路径无效: %v", pathErr)
				}
				data, readErr := io.ReadAll(io.LimitReader(r, 4097))
				r.Close()
				target := string(data)
				if readErr != nil || len(data) == 0 || len(data) > 4096 || strings.ContainsAny(target, "\\:\x00") || filepath.IsAbs(target) || !contained(stage, filepath.Join(filepath.Dir(p), target)) {
					return fmt.Errorf("应用 ZIP 包含越界或无效符号链接")
				}
				entries++
				if entries > 200000 {
					return fmt.Errorf("安装包文件数量超出限制")
				}
				if e = mkdir(filepath.Dir(p)); e != nil {
					return e
				}
				// Delay links until regular files have been written, and block all
				// later writes underneath these paths. This preserves .app bundles.
				links[p] = true
				pending = append(pending, zipLink{p, target})
				continue
			}
			e = write(entry.Name, entry.Mode(), int64(entry.UncompressedSize64), r)
			r.Close()
			if e != nil {
				return e
			}
		}
		for _, link := range pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := os.Symlink(link.target, link.path); err != nil {
				return err
			}
		}
		for _, link := range pending {
			resolved, err := filepath.EvalSymlinks(link.path)
			if err != nil || !contained(stage, resolved) {
				return fmt.Errorf("应用 ZIP 的符号链接链无效或越界")
			}
		}
	} else {
		f, e := os.Open(file)
		if e != nil {
			return e
		}
		defer f.Close()
		gz, e := gzip.NewReader(f)
		if e != nil {
			return e
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			header, e := tr.Next()
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
			switch header.Typeflag {
			case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
				if e = write(header.Name, header.FileInfo().Mode(), header.Size, tr); e != nil {
					return e
				}
			case tar.TypeSymlink:
				p, e := archivePath(stage, header.Name, strip, links)
				if e != nil {
					return e
				}
				if p == "" {
					continue
				}
				target := filepath.Clean(filepath.Join(filepath.Dir(p), filepath.FromSlash(header.Linkname)))
				if filepath.IsAbs(header.Linkname) || strings.HasPrefix(strings.ReplaceAll(header.Linkname, "\\", "/"), "/") || strings.Contains(header.Linkname, ":") || !contained(stage, target) {
					return fmt.Errorf("安装包符号链接越界")
				}
				if e = mkdir(filepath.Dir(p)); e != nil {
					return e
				}
				if e = os.Symlink(header.Linkname, p); e != nil {
					return e
				}
				links[p] = true
			default:
				return fmt.Errorf("安装包包含不支持的条目类型 %d", header.Typeflag)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(stage, dest)
}
