package agent

import (
	"blux/internal/platform"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ReasoningOption struct {
	ID          string `json:"id"`
	Description string `json:"description,omitempty"`
}

type ModelInfo struct {
	ReasoningEfforts []ReasoningOption `json:"reasoning_efforts,omitempty"`
	DefaultEffort    string            `json:"default_effort,omitempty"`
	ID               string            `json:"id"`
	Model            string            `json:"model"`
	Provider         string            `json:"provider,omitempty"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Source           string            `json:"source"`
}
type ModelCatalog struct {
	Models          []ModelInfo `json:"models"`
	DefaultEffort   string      `json:"default_effort,omitempty"`
	DefaultModel    string      `json:"default_model,omitempty"`
	DefaultProvider string      `json:"default_provider,omitempty"`
	Reason          string      `json:"reason,omitempty"`
	Source          string      `json:"source"`
}

func canonicalModel(v string) string { return strings.ReplaceAll(strings.TrimSpace(v), "[1M]", "[1m]") }
func (c *ModelCatalog) add(m ModelInfo) {
	m.Model = canonicalModel(m.Model)
	if m.Model == "" || len(m.Model) > 256 {
		return
	}
	if m.ID == "" {
		m.ID = m.Model
	}
	if m.Name == "" {
		m.Name = m.Model
	}
	for i, v := range c.Models {
		if v.Model == m.Model && v.Provider == m.Provider {
			if len(v.ReasoningEfforts) == 0 {
				c.Models[i].ReasoningEfforts = m.ReasoningEfforts
			}
			if v.DefaultEffort == "" {
				c.Models[i].DefaultEffort = m.DefaultEffort
			}
			return
		}
	}
	c.Models = append(c.Models, m)
}

// Catalog discovery initializes native metadata protocols only. No prompts or
// turns are submitted, and account/credential fields never leave this boundary.
func ListModels(ctx context.Context, id, binary, workspace string) (ModelCatalog, error) {
	c := ModelCatalog{Models: []ModelInfo{}}
	switch id {
	case "codex":
		info, e := ProbeCodex(ctx, binary)
		if e != nil {
			return c, e
		}
		cmd, e := platform.CodexCommand(nil, info.Path, "app-server", "--listen", "stdio://")
		if e != nil {
			return c, e
		}
		cmd.Dir = workspace
		e = withMetadata(ctx, cmd, func(p *metadataPipe) error {
			if e := p.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "blux-models", "version": "0.1.0"}}, nil); e != nil {
				return e
			}
			if e := p.send(map[string]any{"method": "initialized"}); e != nil {
				return e
			}
			var config struct {
				Config struct {
					Model    string `json:"model"`
					Provider string `json:"model_provider"`
					Effort   string `json:"model_reasoning_effort"`
				} `json:"config"`
			}
			if e := p.call("config/read", map[string]any{"cwd": workspace, "includeLayers": false}, &config); e == nil {
				c.DefaultModel = config.Config.Model
				c.DefaultEffort = config.Config.Effort
			}
			cursor := ""
			for page := 0; page < 8; page++ {
				var list struct {
					Data []struct {
						Model       string `json:"model"`
						Name        string `json:"displayName"`
						Description string `json:"description"`
						Default     bool   `json:"isDefault"`
						Hidden      bool   `json:"hidden"`
						Efforts     []struct {
							ID          string `json:"reasoningEffort"`
							Description string `json:"description"`
						} `json:"supportedReasoningEfforts"`
						DefaultEffort string `json:"defaultReasoningEffort"`
					} `json:"data"`
					Next string `json:"nextCursor"`
				}
				params := map[string]any{"limit": 100, "includeHidden": false}
				if cursor != "" {
					params["cursor"] = cursor
				}
				if e := p.call("model/list", params, &list); e != nil {
					return e
				}
				for _, m := range list.Data {
					if m.Hidden {
						continue
					}
					item := ModelInfo{Model: m.Model, Name: m.Name, Description: m.Description, Source: "codex", DefaultEffort: m.DefaultEffort}
					for _, v := range m.Efforts {
						item.ReasoningEfforts = append(item.ReasoningEfforts, ReasoningOption{ID: v.ID, Description: v.Description})
					}
					c.add(item)
					if c.DefaultModel == "" && m.Default {
						c.DefaultModel = m.Model
					}
				}
				if list.Next == "" {
					break
				}
				if list.Next == cursor {
					return errors.New("repeated model catalog cursor")
				}
				cursor = list.Next
			}
			if c.DefaultModel != "" {
				c.add(ModelInfo{Model: c.DefaultModel, Source: "cli_config"})
			}
			c.Source = "codex"
			return nil
		})
		return c, e
	case "claude":
		info, e := ProbeClaude(ctx, binary)
		if e != nil {
			return c, e
		}
		env, e := claudeEnvironment()
		if e != nil {
			return c, e
		}
		cmd := exec.Command(info.Path, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--restricted", "--tools", "", "--permission-mode", "dontAsk", "--permission-prompts", "none", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--settings", `{"disableAllHooks":true}`, "--system-prompt-snapshot", "off")
		cmd.Dir = workspace
		cmd.Env = env
		e = withMetadata(ctx, cmd, func(p *metadataPipe) error {
			if e := p.send(map[string]any{"type": "control_request", "request_id": "catalog", "request": map[string]any{"subtype": "initialize", "hooks": map[string]any{}}}); e != nil {
				return e
			}
			for {
				f, e := p.read()
				if e != nil {
					return e
				}
				if f.Type != "control_response" {
					continue
				}
				var r struct {
					Subtype   string `json:"subtype"`
					RequestID string `json:"request_id"`
					Response  struct {
						Models []struct {
							Value          string   `json:"value"`
							Resolved       string   `json:"resolvedModel"`
							Name           string   `json:"displayName"`
							Description    string   `json:"description"`
							SupportsEffort bool     `json:"supportsEffort"`
							Efforts        []string `json:"supportedEffortLevels"`
						} `json:"models"`
					} `json:"response"`
				}
				if json.Unmarshal(f.Response, &r) != nil || r.RequestID != "catalog" {
					continue
				}
				if r.Subtype != "success" {
					return errors.New("Claude model metadata initialization failed")
				}
				for _, m := range r.Response.Models {
					model := m.Resolved
					if model == "" {
						model = m.Value
					}
					if m.Value == "default" {
						c.DefaultModel = canonicalModel(model)
					}
					name := m.Name
					if strings.HasSuffix(canonicalModel(model), "[1m]") && !strings.Contains(strings.ToLower(name), "1m") {
						name += " · 1M"
					}
					item := ModelInfo{Model: model, Name: name, Description: m.Description, Source: "claude"}
					if m.SupportsEffort {
						for _, level := range m.Efforts {
							item.ReasoningEfforts = append(item.ReasoningEfforts, ReasoningOption{ID: level})
						}
					}
					c.add(item)
				}
				c.Source = "claude"
				if c.DefaultModel != "" {
					c.add(ModelInfo{Model: c.DefaultModel, Source: "cli_config"})
				}
				return nil
			}
		})
		return c, e
	case "deepseek":
		info, e := (Headless{ID: id, Binary: binary}).Probe(ctx)
		if e != nil {
			return c, e
		}
		profile, e := readDeepSeekProfile(ctx, info.Path)
		if e != nil {
			return c, e
		}
		return profile.Catalog, nil
	case "gemini":
		info, e := (Headless{ID: id, Binary: binary}).Probe(ctx)
		if e != nil {
			return c, e
		}
		cmd := exec.Command(info.Path, "--experimental-acp")
		cmd.Dir = workspace
		e = withMetadata(ctx, cmd, func(p *metadataPipe) error {
			if e := p.call("initialize", map[string]any{"protocolVersion": 1, "clientInfo": map[string]string{"name": "blux-models", "version": "0.1.0"}, "clientCapabilities": map[string]any{}}, nil); e != nil {
				return e
			}
			var session struct {
				ID     string `json:"sessionId"`
				Models struct {
					Current   string `json:"currentModelId"`
					Available []struct {
						ID          string `json:"modelId"`
						Name        string `json:"name"`
						Description string `json:"description"`
					} `json:"availableModels"`
				} `json:"models"`
			}
			if e := p.call("session/new", map[string]any{"cwd": workspace, "mcpServers": []any{}}, &session); e != nil {
				return e
			}
			c.DefaultModel = session.Models.Current
			for _, m := range session.Models.Available {
				c.add(ModelInfo{Model: m.ID, Name: m.Name, Description: m.Description, Source: "gemini"})
			}
			c.Source = "gemini"
			return nil
		})
		return c, e
	}
	return c, errors.New("unsupported agent model catalog")
}

type metadataFrame struct {
	Type     string          `json:"type"`
	ID       json.RawMessage `json:"id"`
	Method   string          `json:"method"`
	Result   json.RawMessage `json:"result"`
	Response json.RawMessage `json:"response"`
	Error    *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type metadataPipe struct {
	ctx     context.Context
	in      io.Writer
	frames  <-chan metadataFrame
	failure <-chan error
	id      int
}

func (p *metadataPipe) send(v any) error { return json.NewEncoder(p.in).Encode(v) }
func (p *metadataPipe) read() (metadataFrame, error) {
	select {
	case <-p.ctx.Done():
		return metadataFrame{}, p.ctx.Err()
	case f, ok := <-p.frames:
		if ok {
			return f, nil
		}
		select {
		case e := <-p.failure:
			if e != nil {
				return f, e
			}
		default:
		}
		return f, io.EOF
	}
}
func (p *metadataPipe) call(method string, params, target any) error {
	p.id++
	id := p.id
	if e := p.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); e != nil {
		return e
	}
	for {
		f, e := p.read()
		if e != nil {
			return e
		}
		if f.Method != "" {
			if len(f.ID) > 0 {
				_ = p.send(map[string]any{"jsonrpc": "2.0", "id": f.ID, "error": map[string]any{"code": -32601, "message": "metadata client has no tool capabilities"}})
			}
			continue
		}
		var responseID int
		if json.Unmarshal(f.ID, &responseID) != nil || responseID != id {
			continue
		}
		if f.Error != nil {
			return errors.New(f.Error.Message)
		}
		if target == nil {
			return nil
		}
		return json.Unmarshal(f.Result, target)
	}
}
func withMetadata(parent context.Context, cmd *exec.Cmd, run func(*metadataPipe) error) error {
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	platform.ConfigureProcess(cmd)
	cmd.Stderr = &codexBoundedBuffer{limit: 4096}
	in, e := cmd.StdinPipe()
	if e != nil {
		return e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		in.Close()
		return e
	}
	if e = cmd.Start(); e != nil {
		in.Close()
		out.Close()
		return e
	}
	release, e := platform.AttachProcessTree(cmd)
	if e != nil {
		platform.TerminateTree(cmd, true)
		cmd.Wait()
		return e
	}
	defer release()
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			platform.TerminateTree(cmd, true)
		case <-stopped:
		}
	}()
	defer close(stopped)
	frames := make(chan metadataFrame, 32)
	failure := make(chan error, 1)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		defer close(frames)
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 64<<10), codexMaxFrameBytes)
		for scan.Scan() {
			var f metadataFrame
			if json.Unmarshal(scan.Bytes(), &f) != nil {
				failure <- errors.New("invalid CLI metadata response")
				return
			}
			select {
			case frames <- f:
			case <-ctx.Done():
				return
			}
		}
		failure <- scan.Err()
	}()
	defer func() {
		cancel()
		in.Close()
		force := platform.BeginTermination(cmd)
		force()
		cmd.Wait()
		out.Close()
		<-readDone
	}()
	return run(&metadataPipe{ctx: ctx, in: in, frames: frames, failure: failure})
}
func CatalogWorkspace(dataDir string) (string, error) {
	p := filepath.Join(dataDir, "catalog")
	e := os.MkdirAll(p, 0700)
	return p, e
}
