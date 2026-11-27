package oxgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type targetOwner struct {
	Path string `json:"path"`
	Tool Tool   `json:"tool"`
}

type versionedTarget struct {
	owner         targetOwner
	previous      os.FileInfo
	marker        string
	hasMarker     bool
	createdMarker bool
	stage         string
	backup        string
	written       bool
}

func newVersionedTarget(p InstallPlan) (*versionedTarget, error) {
	if !filepath.IsAbs(p.InstallDir) {
		return nil, fmt.Errorf("版本目录必须是绝对路径")
	}
	dir := filepath.Clean(p.InstallDir)
	parent := filepath.Dir(dir)
	if parent == dir {
		return nil, fmt.Errorf("版本目录不能是磁盘根目录")
	}
	t := &versionedTarget{
		owner:  targetOwner{dir, p.Tool},
		marker: filepath.Join(parent, ".oxgen-target-"+filepath.Base(dir)+".json"),
	}
	info, err := os.Lstat(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	t.previous = info
	marker, err := os.ReadFile(t.marker)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		var previous targetOwner
		t.hasMarker = json.Unmarshal(marker, &previous) == nil && samePath(previous.Path, dir) && previous.Tool == p.Tool
		if !t.hasMarker {
			return nil, fmt.Errorf("版本目录的 OXGEN 标记不匹配，未覆盖: %s", dir)
		}
	}
	if info != nil && (!info.IsDir() || !t.hasMarker) {
		return nil, fmt.Errorf("所选版本目录已存在且不是可确认的 OXGEN 安装，未覆盖: %s", dir)
	}
	if err = os.MkdirAll(parent, 0755); err != nil {
		return nil, err
	}
	t.stage, err = os.MkdirTemp(parent, ".oxgen-stage-*")
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Defer moving the old directory until we have a verified payload or are ready
// to run an installer that generates files bound to the final path.
func (t *versionedTarget) reserve() error {
	path := t.owner.Path
	current, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if t.previous == nil && current == nil {
		return nil
	}
	if t.previous == nil || current == nil || !current.IsDir() || !os.SameFile(current, t.previous) {
		return fmt.Errorf("安装目标在作业期间发生变化，请重新检测: %s", path)
	}
	backup := path + ".previous-" + strings.TrimPrefix(filepath.Base(t.stage), ".oxgen-stage-")
	if _, err = os.Lstat(backup); err == nil {
		return fmt.Errorf("备份目录已存在: %s", backup)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.Rename(path, backup); err != nil {
		return err
	}
	t.backup = backup
	return nil
}

func (t *versionedTarget) rollback() error {
	path := t.owner.Path
	if t.written {
		if err := removeTemp(filepath.Dir(path), path); err != nil {
			return fmt.Errorf("无法清理未完成的安装 %s（备份：%s）: %w", path, t.backup, err)
		}
	}
	if t.backup != "" {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fmt.Errorf("无法恢复原目录 %s，旧程序保存在 %s", path, t.backup)
		}
		if err := os.Rename(t.backup, path); err != nil {
			return fmt.Errorf("无法恢复原目录 %s，旧程序保存在 %s: %w", path, t.backup, err)
		}
	}
	if t.createdMarker {
		return os.Remove(t.marker)
	}
	return nil
}

func (a *App) installVersioned(ctx context.Context, p InstallPlan, out io.Writer, save func(Record) error) (record Record, err error) {
	t, err := newVersionedTarget(p)
	if err != nil {
		return Record{}, err
	}
	defer func() {
		if err != nil {
			if rollbackErr := t.rollback(); rollbackErr != nil {
				// Report an incomplete rollback even if cancellation caused it.
				err = fmt.Errorf("%v；恢复安装失败: %w", err, rollbackErr)
			}
		}
		if cleanupErr := removeTemp(filepath.Dir(t.stage), t.stage); cleanupErr != nil {
			fmt.Fprintln(out, "无法清理本次暂存目录：", t.stage, cleanupErr)
		}
	}()
	if p.Tool == Python && a.Host.OS == "windows" {
		manager, index, e := a.downloadWindowsPython(ctx, p, out)
		if e != nil {
			return Record{}, e
		}
		if err = ctx.Err(); err != nil {
			return Record{}, err
		}
		if err = t.reserve(); err != nil {
			return Record{}, err
		}
		// Console entry points from ensurepip can contain the absolute path
		// to python.exe, so install and validate this payload at its final path.
		t.written = true
		record, err = a.installWindowsPython(ctx, p, manager, index, out)
		if err != nil {
			return Record{}, err
		}
		if err = ctx.Err(); err != nil {
			return Record{}, err
		}
	} else {
		staged := p
		staged.InstallDir = filepath.Join(t.stage, "payload")
		record, err = a.installPayload(ctx, staged, out)
		if err != nil {
			return Record{}, err
		}
		if !contained(staged.InstallDir, record.Path) {
			return Record{}, errors.New("已验证的命令入口不在本次安装目录内")
		}
		for _, path := range []*string{&record.Path, &record.Runtime, &record.Entry} {
			if contained(staged.InstallDir, *path) {
				relative, err := filepath.Rel(staged.InstallDir, *path)
				if err != nil {
					return Record{}, err
				}
				*path = filepath.Join(t.owner.Path, relative)
			}
		}
		if err = ctx.Err(); err != nil {
			return Record{}, err
		}
		if err = t.reserve(); err != nil {
			return Record{}, err
		}
		if err = os.Rename(staged.InstallDir, t.owner.Path); err != nil {
			return Record{}, err
		}
		t.written = true
	}
	// Once the replacement is in place, finish recording it even if the user
	// has just requested to stop the remaining work.
	if !t.hasMarker {
		if err = saveJSON(t.marker, t.owner); err != nil {
			return Record{}, fmt.Errorf("保存安装目录标记失败: %w", err)
		}
		t.createdMarker = true
	}
	if save != nil {
		if err = save(record); err != nil {
			return Record{}, fmt.Errorf("保存安装记录失败，回退本次安装: %w", err)
		}
	}
	return record, nil
}
