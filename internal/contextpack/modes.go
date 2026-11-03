package contextpack

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed modes/*.json
var files embed.FS

type Mode struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Version      int    `json:"version"`
	Instructions string `json:"instructions"`
}

func Get(id string) (Mode, error) {
	if id == "" {
		id = "explore"
	}
	var m Mode
	switch id {
	case "spec", "development", "analysis", "explore":
	default:
		return m, fmt.Errorf("unknown mode")
	}
	b, e := files.ReadFile("modes/" + id + ".json")
	if e == nil {
		e = json.Unmarshal(b, &m)
	}
	return m, e
}
func List() []Mode {
	out := []Mode{}
	for _, id := range []string{"spec", "development", "analysis", "explore"} {
		m, _ := Get(id)
		out = append(out, m)
	}
	return out
}
