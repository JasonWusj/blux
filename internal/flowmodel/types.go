package flowmodel

import (
	"blux/internal/contextpack"
	"blux/internal/core"
	"encoding/json"
	"fmt"
	"regexp"
)

type UnitTemplate struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}
type PhaseTemplate struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Mode      string         `json:"mode"`
	DependsOn []string       `json:"depends_on"`
	Gate      bool           `json:"gate"`
	Verify    bool           `json:"verify"`
	Units     []UnitTemplate `json:"units"`
}
type Template struct {
	ID      string          `json:"id"`
	Version int             `json:"version"`
	Name    string          `json:"name"`
	Phases  []PhaseTemplate `json:"phases"`
}
type Unit struct {
	ID             string   `json:"id"`
	TemplateID     string   `json:"template_id"`
	Name           string   `json:"name"`
	Prompt         string   `json:"prompt"`
	TaskID         string   `json:"task_id"`
	SnapshotID     string   `json:"snapshot_id"`
	InputArtifacts []string `json:"input_artifacts"`
	Status         string   `json:"status"`
	Integrated     bool     `json:"integrated"`
}
type Attempt struct {
	ID       string `json:"id"`
	Number   int    `json:"number"`
	Units    []Unit `json:"units"`
	Feedback string `json:"feedback"`
}
type Phase struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Mode       string   `json:"mode"`
	DependsOn  []string `json:"depends_on"`
	Gate       bool     `json:"gate"`
	Verify     bool     `json:"verify"`
	Status     string   `json:"status"`
	Attempt    Attempt  `json:"attempt"`
	Checkpoint string   `json:"checkpoint"`
}
type FlowRun struct {
	ID         string  `json:"id"`
	Generation int64   `json:"generation"`
	Phases     []Phase `json:"phases"`
}
type Delivery struct {
	ID               string    `json:"id"`
	Goal             string    `json:"goal"`
	RepositoryPath   string    `json:"repository_path"`
	Adapter          string    `json:"adapter"`
	Template         Template  `json:"template"`
	ContextIDs       []string  `json:"context_ids"`
	BaseCommit       string    `json:"base_commit"`
	Head             string    `json:"head"`
	Branch           string    `json:"branch"`
	Worktree         string    `json:"worktree"`
	Run              FlowRun   `json:"run"`
	History          []FlowRun `json:"history"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason"`
	Revision         int64     `json:"revision"`
	Reworks          int       `json:"reworks"`
	AcceptanceCommit string    `json:"acceptance_commit"`
	AcceptanceBase   string    `json:"acceptance_base"`
	IntegrationPath  string    `json:"integration_path"`
}

func Default() Template {
	return Template{ID: "design-develop-verify", Version: 1, Name: "设计 → 开发 → 验证", Phases: []PhaseTemplate{{ID: "design", Name: "方案设计", Mode: "spec", Gate: true, Units: []UnitTemplate{{ID: "spec", Name: "需求与方案", Prompt: "根据交付目标制定规格和实施方案，产出 Spec.md，等待人工验收。"}}}, {ID: "develop", Name: "开发", Mode: "development", DependsOn: []string{"design"}, Units: []UnitTemplate{{ID: "code", Name: "编码实现", Prompt: "根据已验收的精确 Spec 版本实现目标并验证。"}}}, {ID: "verify", Name: "验证", Mode: "development", DependsOn: []string{"develop"}, Gate: true, Verify: true, Units: []UnitTemplate{{ID: "test", Name: "独立验证", Prompt: "独立检查规格、代码及测试。将验证证据写入 Verification.md，将结论写入 Verification.json，格式为 {\"passed\":true或false,\"feedback\":\"问题和证据\"}。不要降低标准或伪造通过。"}}}}}
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

func Validate(t Template) error {
	if !identifier.MatchString(t.ID) || t.Version < 1 || len(t.Phases) < 1 || len(t.Phases) > 12 {
		return core.ErrInvalidState
	}
	known := map[string]bool{}
	for _, p := range t.Phases {
		if !identifier.MatchString(p.ID) || known[p.ID] || len(p.Units) < 1 || len(p.Units) > 16 {
			return core.ErrInvalidState
		}
		if _, e := contextpack.Get(p.Mode); e != nil {
			return core.ErrInvalidState
		}
		for _, dep := range p.DependsOn {
			if !known[dep] {
				return fmt.Errorf("dependencies must refer to earlier phases: %w", core.ErrInvalidState)
			}
		}
		units := map[string]bool{}
		for _, u := range p.Units {
			if !identifier.MatchString(u.ID) || units[u.ID] || u.Prompt == "" {
				return core.ErrInvalidState
			}
			units[u.ID] = true
		}
		known[p.ID] = true
	}
	return nil
}
func NewRun(t Template, generation int64) FlowRun {
	r := FlowRun{ID: core.NewID("flow"), Generation: generation, Phases: []Phase{}}
	for _, p := range t.Phases {
		phase := Phase{ID: p.ID, Name: p.Name, Mode: p.Mode, DependsOn: p.DependsOn, Gate: p.Gate, Verify: p.Verify, Status: "pending", Attempt: Attempt{ID: core.NewID("attempt"), Number: 1, Units: []Unit{}}}
		for _, u := range p.Units {
			phase.Attempt.Units = append(phase.Attempt.Units, Unit{ID: core.NewID("unit"), TemplateID: u.ID, Name: u.Name, Prompt: u.Prompt, Status: "pending", InputArtifacts: []string{}})
		}
		r.Phases = append(r.Phases, phase)
	}
	return r
}
func Clone(d Delivery) Delivery {
	b, _ := json.Marshal(d)
	var copy Delivery
	json.Unmarshal(b, &copy)
	return copy
}
