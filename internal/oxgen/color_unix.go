//go:build linux || darwin

package oxgen

import (
	"io"
	"os"
)

func enableColor(out io.Writer) bool {
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	return err == nil && stat.Mode()&os.ModeCharDevice != 0
}

func terminalInput(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	stat, err := f.Stat()
	return err == nil && stat.Mode()&os.ModeCharDevice != 0
}
