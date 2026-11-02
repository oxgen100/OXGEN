//go:build windows

package oxgen

import (
	"fmt"
	"syscall"
	"unsafe"
)

func checkSpace(path string, need int64) error {
	p, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	var available, total, free uint64
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")
	ok, _, err := proc.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free)))
	if ok == 0 {
		return fmt.Errorf("无法检查磁盘空间: %v", err)
	}
	if need > 0 && available < uint64(need) {
		return fmt.Errorf("磁盘可用空间不足：需要约 %.0f MiB，可用 %.0f MiB", float64(need)/1048576, float64(available)/1048576)
	}
	return nil
}
