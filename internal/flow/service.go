package flow

import (
	"blux/internal/artifact"
	"blux/internal/core"
	"blux/internal/flowmodel"
	"blux/internal/platform"
	"blux/internal/store"
	"blux/internal/workspace"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type Service struct {
	Store   *store.Store
	DataDir string
}
type CreateInput struct {
	Goal           string             `json:"goal"`
	RepositoryPath string             `json:"repository_path"`
	Adapter        string             `json:"adapter"`
	BaseCommit     string             `json:"base_commit"`
	Template       flowmodel.Template `json:"template"`
	ContextIDs     []string           `json:"context_ids"`
}
type Action struct {
	Kind        string   `json:"kind"`
	PhaseID     string   `json:"phase_id"`
	Generation  int64    `json:"generation"`
	SnapshotIDs []string `json:"snapshot_ids"`
	Feedback    string   `json:"feedback"`
}

func hash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func (s Service) lock(id string) (func(), error) {
	if id != "create" {
		if !strings.HasPrefix(id, "delivery_") || len(id) != 41 {
			return nil, core.ErrInvalidState
		}
		if _, e := hex.DecodeString(id[9:]); e != nil {
			return nil, core.ErrInvalidState
		}
	}
	if e := os.MkdirAll(filepath.Join(s.DataDir, "locks"), 0700); e != nil {
		return nil, e
	}
	return platform.Lock(filepath.Join(s.DataDir, "locks", "flow-"+id+".lock"))
}
func (s Service) Create(ctx context.Context, in CreateInput, key string) (flowmodel.Delivery, error) {
	var empty flowmodel.Delivery
	if key == "" || strings.TrimSpace(in.Goal) == "" {
		return empty, core.ErrInvalidState
	}
	if in.Template.ID == "" {
		in.Template = flowmodel.Default()
	}
	if e := flowmodel.Validate(in.Template); e != nil {
		return empty, e
	}
	if in.Adapter == "" {
		in.Adapter = "codex"
	}
	if in.Adapter != "codex" {
		return empty, core.ErrInvalidState
	}
	digestInput := in
	digestInput.BaseCommit = ""
	h := hash(digestInput)
	unlock, e := s.lock("create")
	if e != nil {
		return empty, core.ErrConflict
	}
	defer unlock()
	if d, found, e := s.Store.FlowReceipt(ctx, "create", key, h); found || e != nil {
		return d, e
	}
	d := flowmodel.Delivery{ID: core.NewID("delivery"), Goal: in.Goal, RepositoryPath: in.RepositoryPath, Adapter: in.Adapter, BaseCommit: in.BaseCommit, Head: in.BaseCommit, Template: in.Template, ContextIDs: in.ContextIDs, Run: flowmodel.NewRun(in.Template, 1), History: []flowmodel.FlowRun{}, Status: "running"}
	return s.Store.SaveDelivery(ctx, d, 0, "create", key, h)
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prefix := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "core.fsmonitor=false", "-c", "user.name=Blux", "-c", "user.email=blux@localhost", "-C", dir}
	cmd := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	b, e := cmd.Output()
	return strings.TrimSpace(string(b)), e
}
func (s Service) prepare(ctx context.Context, d *flowmodel.Delivery) error {
	if d.Adapter != "codex" {
		return nil
	}
	if d.Worktree != "" {
		return nil
	}
	path := filepath.Join(s.DataDir, "deliveries", d.ID, fmt.Sprintf("generation-%d", d.Run.Generation))
	branch := fmt.Sprintf("feat/blux-%s-g%d", d.ID, d.Run.Generation)
	repo, e := (workspace.Manager{DataDir: s.DataDir}).Resolve(ctx, d.RepositoryPath, d.BaseCommit)
	if e != nil {
		return e
	}
	if _, e := os.Stat(path); os.IsNotExist(e) {
		if _, e = git(ctx, repo.Root, "worktree", "add", "-b", branch, path, d.Head); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	b, e := git(ctx, path, "symbolic-ref", "--short", "HEAD")
	if e != nil || b != branch {
		return errors.New("delivery worktree identity mismatch")
	}
	d.Worktree = path
	d.Branch = branch
	return nil
}
func (s Service) Tick(ctx context.Context) error {
	list, e := s.Store.Deliveries(ctx)
	if e != nil {
		return e
	}
	for _, d := range list {
		if d.Status != "running" {
			continue
		}
		unlock, e := s.lock(d.ID)
		if e != nil {
			continue
		}
		e = s.advance(ctx, d.ID)
		unlock()
		if errors.Is(e, core.ErrNotFound) {
			latest, readErr := s.Store.Delivery(ctx, d.ID)
			if readErr == nil {
				deleted := false
				for _, p := range latest.Run.Phases {
					for _, u := range p.Attempt.Units {
						if u.TaskID != "" {
							gone, err := s.Store.IsTaskDeleted(ctx, u.TaskID)
							if err == nil && gone {
								deleted = true
							}
						}
					}
				}
				if deleted {
					continue
				}
			}
		}
		if e != nil && !errors.Is(e, core.ErrConflict) {
			return e
		}
	}
	return nil
}
func (s Service) advance(ctx context.Context, id string) error {
	d, e := s.Store.Delivery(ctx, id)
	if e != nil {
		return e
	}
	before := flowmodel.Clone(d)
	if d.Status != "running" {
		return nil
	}
	for _, phase := range d.Run.Phases {
		for _, unit := range phase.Attempt.Units {
			if unit.TaskID == "" {
				continue
			}
			deleted, err := s.Store.IsTaskDeleted(ctx, unit.TaskID)
			if err != nil {
				return err
			}
			if deleted {
				d.Status = "blocked"
				d.Reason = "阶段任务已删除，请返工后继续。"
				_, err = s.Store.SaveDelivery(ctx, d, before.Revision, "", "", "")
				return err
			}
		}
	}
	if e = s.prepare(ctx, &d); e != nil {
		d.Status = "blocked"
		d.Reason = "交付工作区准备失败：" + e.Error()
	} else {
		for i := range d.Run.Phases {
			p := &d.Run.Phases[i]
			if p.Status == "accepted" || p.Status == "awaiting_review" {
				continue
			}
			ready := true
			for _, dep := range p.DependsOn {
				for _, prior := range d.Run.Phases {
					if prior.ID == dep && prior.Status != "accepted" {
						ready = false
					}
				}
			}
			if !ready {
				continue
			}
			inputs := []string{}
			for _, upstream := range d.Run.Phases {
				if upstream.Status == "accepted" {
					for _, u := range upstream.Attempt.Units {
						if u.SnapshotID != "" {
							v, err := s.Store.Snapshot(ctx, u.SnapshotID)
							if err != nil {
								return err
							}
							for _, a := range v.Artifacts {
								inputs = append(inputs, a.ID)
							}
						}
					}
				}
			}
			sort.Strings(inputs)
			p.Status = "running"
			all := true
			for j := range p.Attempt.Units {
				u := &p.Attempt.Units[j]
				if u.TaskID == "" {
					u.InputArtifacts = inputs
					prompt := d.Goal + "\n\n当前单元：" + u.Prompt + "\n\n反馈：" + p.Attempt.Feedback + "\n精确交接产物已写入 .agent/inputs/，清单见 .agent/inputs.json。"
					task, _, err := s.Store.CreateTask(ctx, core.CreateTaskInput{Title: p.Name + " · " + u.Name, Prompt: prompt, Adapter: d.Adapter, RepositoryPath: d.RepositoryPath, BaseCommit: d.Head, Mode: p.Mode, ContextIDs: d.ContextIDs, InputArtifacts: inputs, DeliveryID: d.ID, FlowGeneration: d.Run.Generation, IdempotencyKey: "flow:" + u.ID})
					if err != nil {
						return err
					}
					u.TaskID = task.Task.ID
					u.Status = "queued"
				}
				task, err := s.Store.GetTask(ctx, u.TaskID)
				if err != nil {
					return err
				}
				u.Status = task.Task.Status
				if u.Status == "failed" {
					d.Status = "blocked"
					d.Reason = p.Name + " / " + u.Name + " 执行未完成，请查看任务后返工。"
					all = false
					break
				}
				if u.Status != "completed" {
					all = false
					continue
				}
				if u.SnapshotID == "" {
					versions, err := s.Store.Snapshots(ctx, u.TaskID)
					if err != nil {
						return err
					}
					if len(versions) == 0 {
						d.Status = "blocked"
						d.Reason = "成功任务缺少产物快照。"
						all = false
						break
					}
					v := versions[len(versions)-1]
					if v.TurnID != task.Task.CurrentTurnID {
						return core.ErrConflict
					}
					u.SnapshotID = v.ID
				}
				if !u.Integrated {
					v, err := s.Store.Snapshot(ctx, u.SnapshotID)
					if err != nil {
						return err
					}
					if d.Adapter == "codex" && v.Commit != "" {
						if _, err = git(ctx, d.Worktree, "merge-base", "--is-ancestor", v.Commit, "HEAD"); err != nil {
							if _, err = git(ctx, d.Worktree, "merge", "--no-ff", "-m", "Integrate "+u.ID, v.Commit); err != nil {
								d.Status = "blocked"
								d.Reason = "并行代码整合冲突，保留在 " + d.Worktree
								all = false
								break
							}
						}
						d.Head, err = git(ctx, d.Worktree, "rev-parse", "HEAD")
						if err != nil {
							return err
						}
					}
					u.Integrated = true
				}
			}
			if d.Status == "blocked" {
				break
			}
			if all {
				p.Checkpoint = d.Head
				if p.Verify {
					failed, feedback, err := s.verdict(ctx, *p)
					if err != nil {
						return err
					}
					if failed {
						if d.Reworks >= 3 {
							d.Status = "awaiting_rework"
							d.Reason = "自动返工已达 3 次，请人工处理：" + feedback
							break
						}
						target := 0
						for k := 0; k < i; k++ {
							if d.Run.Phases[k].Mode == "development" && !d.Run.Phases[k].Verify {
								target = k
								break
							}
						}
						d.Reworks++
						s.reset(&d, target, feedback)
						break
					}
				}
				if p.Gate {
					p.Status = "awaiting_review"
				} else {
					p.Status = "accepted"
				}
			}
		}
		complete := d.Status == "running"
		for _, p := range d.Run.Phases {
			if p.Status != "accepted" {
				complete = false
			}
		}
		if complete {
			d.Status = "completed"
		}
	}
	if reflect.DeepEqual(before, d) {
		return nil
	}
	_, e = s.Store.SaveDelivery(ctx, d, before.Revision, "", "", "")
	return e
}
func (s Service) verdict(ctx context.Context, p flowmodel.Phase) (bool, string, error) {
	for _, u := range p.Attempt.Units {
		v, e := s.Store.Snapshot(ctx, u.SnapshotID)
		if e != nil {
			return false, "", e
		}
		for _, a := range v.Artifacts {
			if filepath.Base(a.Path) != "Verification.json" {
				continue
			}
			b, e := os.ReadFile((artifact.Service{DataDir: s.DataDir}).Blob(a.Hash))
			if e != nil {
				return false, "", e
			}
			var verdict struct {
				Passed   *bool  `json:"passed"`
				Feedback string `json:"feedback"`
			}
			if json.Unmarshal(b, &verdict) == nil && verdict.Passed != nil && !*verdict.Passed {
				return true, verdict.Feedback, nil
			}
		}
	}
	return false, "", nil
}
func (s Service) reset(d *flowmodel.Delivery, target int, feedback string) {
	old := d.Run
	d.History = append(d.History, old)
	next := flowmodel.NewRun(d.Template, old.Generation+1)
	for i := 0; i < target; i++ {
		next.Phases[i] = old.Phases[i]
	}
	next.Phases[target].Attempt.Number = old.Phases[target].Attempt.Number + 1
	next.Phases[target].Attempt.Feedback = feedback
	head := d.BaseCommit
	for i := 0; i < target; i++ {
		if old.Phases[i].Status == "accepted" && old.Phases[i].Checkpoint != "" {
			head = old.Phases[i].Checkpoint
		}
	}
	d.Run = next
	d.Status = "running"
	d.Reason = ""
	d.Head = head
	d.Worktree = ""
	d.Branch = ""
}
func phaseVersions(p flowmodel.Phase) []string {
	out := []string{}
	for _, u := range p.Attempt.Units {
		out = append(out, u.SnapshotID)
	}
	sort.Strings(out)
	return out
}
func (s Service) Action(ctx context.Context, id, key string, a Action) (flowmodel.Delivery, error) {
	var empty flowmodel.Delivery
	if key == "" {
		return empty, core.ErrInvalidState
	}
	unlock, e := s.lock(id)
	if e != nil {
		return empty, core.ErrConflict
	}
	defer unlock()
	h := hash(a)
	if d, found, e := s.Store.FlowReceipt(ctx, id, key, h); found || e != nil {
		return d, e
	}
	d, e := s.Store.Delivery(ctx, id)
	if e != nil {
		return d, e
	}
	if d.Run.Generation != a.Generation {
		return d, core.ErrStaleGeneration
	}
	index := -1
	for i, p := range d.Run.Phases {
		if p.ID == a.PhaseID {
			index = i
		}
	}
	old := d.Run
	switch a.Kind {
	case "approve":
		if index < 0 || d.Run.Phases[index].Status != "awaiting_review" || d.Status != "running" {
			return d, core.ErrInvalidState
		}
		versions := append([]string{}, a.SnapshotIDs...)
		sort.Strings(versions)
		if !reflect.DeepEqual(versions, phaseVersions(d.Run.Phases[index])) {
			return d, core.ErrConflict
		}
		for _, u := range d.Run.Phases[index].Attempt.Units {
			task, e := s.Store.GetTask(ctx, u.TaskID)
			if e != nil {
				return d, e
			}
			v, e := s.Store.Snapshot(ctx, u.SnapshotID)
			if e != nil {
				return d, e
			}
			if task.Task.CurrentTurnID != v.TurnID || task.Task.Status != "completed" {
				return d, core.ErrConflict
			}
		}
		d.Run.Phases[index].Status = "accepted"
	case "retry", "rollback":
		if index < 0 {
			return d, core.ErrInvalidState
		}
		s.reset(&d, index, a.Feedback)
	case "resume":
		if d.Status != "blocked" {
			return d, core.ErrInvalidState
		}
		if d.Adapter == "codex" && d.Worktree != "" {
			status, e := git(ctx, d.Worktree, "status", "--porcelain=v1")
			if e != nil || status != "" {
				return d, core.ErrConflict
			}
			if _, e = git(ctx, d.Worktree, "rev-parse", "--verify", "MERGE_HEAD"); e == nil {
				return d, core.ErrConflict
			}
			d.Head, e = git(ctx, d.Worktree, "rev-parse", "HEAD")
			if e != nil {
				return d, e
			}
		}
		d.Status = "running"
		d.Reason = ""
	default:
		return d, core.ErrInvalidState
	}
	result, e := s.Store.SaveDelivery(ctx, d, d.Revision, id, key, h)
	if e == nil && d.Run.Generation != old.Generation {
		for _, p := range old.Phases {
			for _, u := range p.Attempt.Units {
				if u.TaskID != "" {
					task, e := s.Store.GetTask(ctx, u.TaskID)
					if e == nil && (task.Task.Status == "queued" || task.Task.Status == "running") {
						_, _ = s.Store.RequestControl(ctx, u.TaskID, task.Task.CurrentTurnID, fmt.Sprintf("invalidate-%d", d.Run.Generation), "stop", "")
					}
				}
			}
		}
	}
	return result, e
}

func (s Service) LookupCreate(ctx context.Context, in CreateInput, key string) (flowmodel.Delivery, bool, error) {
	if in.Template.ID == "" {
		in.Template = flowmodel.Default()
	}
	if in.Adapter == "" {
		in.Adapter = "codex"
	}
	in.BaseCommit = ""
	return s.Store.FlowReceipt(ctx, "create", key, hash(in))
}
