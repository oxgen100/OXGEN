//go:build windows

package oxgen

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

func enableColor(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	k := syscall.NewLazyDLL("kernel32.dll")
	get := k.NewProc("GetConsoleMode")
	set := k.NewProc("SetConsoleMode")
	var mode uint32
	r, _, _ := get.Call(f.Fd(), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}
	r, _, _ = set.Call(f.Fd(), uintptr(mode|4))
	return r != 0
}

func terminalInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	var mode uint32
	get := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleMode")
	r, _, _ := get.Call(f.Fd(), uintptr(unsafe.Pointer(&mode)))
	return r != 0
}
