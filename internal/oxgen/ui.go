package oxgen

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
)

var errCancelled = errors.New("用户取消")

type UI struct {
	reader          *bufio.Reader
	Out             io.Writer
	Color           bool
	Terminal        bool
	Context         context.Context
	cancelled       atomic.Bool
	pending         chan inputLine
	watch           *inputWatch
	operation       context.Context
	operationCancel context.CancelFunc
}

func newUI(in io.Reader, out io.Writer, color bool) *UI {
	return &UI{reader: bufio.NewReader(in), Out: out, Color: color}
}
func (u *UI) Print(format string, args ...any) { fmt.Fprintf(u.Out, format, args...) }
func (u *UI) Line(s string)                    { fmt.Fprintln(u.Out, s) }
func (u *UI) Accent(s string) string {
	return u.paint("96", s)
}
func (u *UI) paint(code, s string) string {
	if !u.Color {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func (u *UI) Rule() { u.Line(u.Accent(strings.Repeat("-", 24))) }
func (u *UI) Item(key, label string) {
	u.Print("%s%s\n", u.Accent(fmt.Sprintf("%-5s", key+".")), label)
}
func (u *UI) Section(title string) {
	u.Line("")
	u.Line(u.Accent(title))
	u.Rule()
}
func (u *UI) Clear() {
	if u.Terminal {
		u.Print("\x1b[2J\x1b[H")
	}
}
func (u *UI) Pause() error {
	if !u.Terminal {
		return nil
	}
	u.Line("")
	for {
		u.Item("0", "返回上一级（回车也可返回）")
		s, err := u.Ask("请输入选择", "0")
		if err != nil {
			return err
		}
		if s == "0" {
			return nil
		}
		u.Warn("输入 0 返回上一级。")
	}
}
func (u *UI) Status(s string) string {
	switch s {
	case "成功", "保留":
		return u.paint("32", "["+s+"]")
	case "失败":
		return u.paint("31", "["+s+"]")
	case "待用户操作":
		return u.paint("33", "["+s+"]")
	default:
		return "[" + s + "]"
	}
}
func (u *UI) Warn(s string) {
	if u.Color {
		u.Print("\x1b[33m%s\x1b[0m\n", s)
	} else {
		u.Line(s)
	}
}

func (u *UI) Ask(prompt, def string) (string, error) {
	wasWatching := u.watch != nil
	if wasWatching {
		u.stopWatch()
		defer u.startWatch()
	}
	u.Print("%s", prompt)
	if def != "" {
		u.Print(" [%s]", def)
	}
	u.Print(": ")
	ctx := u.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if u.operation != nil {
		ctx = u.operation
	}
	s, err := u.readLine(ctx)
	if err != nil && len(s) == 0 {
		return "", err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		s = def
	}
	if s == "0" && u.operationCancel != nil {
		u.cancelled.Store(true)
		u.operationCancel()
	}
	return s, nil
}

func (u *UI) AskValue(prompt, def string) (string, error) {
	u.Line("0. 返回上一级")
	s, err := u.Ask(prompt, def)
	if s == "0" && err == nil {
		u.cancelled.Store(true)
		return "", errCancelled
	}
	return s, err
}

func (u *UI) Choice(prompt string, options []string, def int) (int, error) {
	for {
		u.Section(prompt)
		for i, s := range options {
			u.Item(fmt.Sprint(i+1), s)
		}
		u.Item("0", "返回上一级")
		u.Rule()
		s, err := u.Ask("请输入选择", fmt.Sprint(def+1))
		if err != nil {
			return 0, err
		}
		if s == "0" {
			u.cancelled.Store(true)
			return 0, errCancelled
		}
		for i := range options {
			if s == fmt.Sprint(i+1) {
				return i, nil
			}
		}
		u.Warn("输入无效，请输入菜单中的编号。")
	}
}

func (u *UI) Confirm(prompt string) bool {
	i, err := u.Choice(prompt, []string{"继续"}, -1)
	return err == nil && i == 0
}

func (u *UI) Banner(version string) {
	u.Line("")
	u.Line(u.Accent("O X G E N"))
	u.Line(u.Accent("氧气邮政员 v" + version))
}
