//go:build linux || darwin

package oxgen

import (
	"fmt"
	"syscall"
)

func checkSpace(path string, need int64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return err
	}
	available := uint64(stat.Bavail) * uint64(stat.Bsize)
	if need > 0 && available < uint64(need) {
		return fmt.Errorf("磁盘可用空间不足：需要约 %.0f MiB，可用 %.0f MiB", float64(need)/1048576, float64(available)/1048576)
	}
	return nil
}
