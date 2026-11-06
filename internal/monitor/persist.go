package monitor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"

	"github.com/wisnuvb/ronda/internal/model"
)

func writeJSON(dir, name string, v any) {
	if dir == "" {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path+".tmp", b, 0o640); err != nil {
		log.Printf("monitor: saving %s: %v", name, err)
		return
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		log.Printf("monitor: saving %s: %v", name, err)
	}
}

func readJSON(dir, name string, v any) {
	if dir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return
	}
	if err := json.Unmarshal(b, v); err != nil {
		log.Printf("monitor: %s is corrupt, ignored: %v", name, err)
	}
}

func loadState(dir string) map[string]model.SiteState {
	m := map[string]model.SiteState{}
	readJSON(dir, "state.json", &m)
	return m
}
