package oxgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func loadState(home string) (State, error) {
	s := State{Schema: 1, Records: map[Tool]Record{}}
	b, err := os.ReadFile(filepath.Join(home, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		// Read the rollback copy if a previous process stopped between renames.
		b, err = os.ReadFile(filepath.Join(home, "state.json.previous"))
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("状态文件无法解析，请保留文件并手动修复: %w", err)
	}
	if s.Schema != 1 {
		return s, fmt.Errorf("不支持状态文件版本 %d", s.Schema)
	}
	if s.Records == nil {
		s.Records = map[Tool]Record{}
	}
	return s, nil
}

func saveJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".oxgen-json-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	// Windows cannot replace an existing file with os.Rename. Keep a rollback copy.
	backup := path + ".previous"
	if _, statErr := os.Stat(path); statErr == nil {
		if _, err = os.Stat(backup); err == nil {
			if err = os.Remove(backup); err != nil {
				return err
			}
		}
		if err = os.Rename(path, backup); err != nil {
			return err
		}
	}
	if err = os.Rename(tmp, path); err != nil {
		_ = os.Rename(backup, path)
		return err
	}
	_ = os.Remove(backup)
	return nil
}

func saveState(home string, s State) error {
	if len(s.History) > 200 {
		s.History = s.History[len(s.History)-200:]
	}
	return saveJSON(filepath.Join(home, "state.json"), s)
}
