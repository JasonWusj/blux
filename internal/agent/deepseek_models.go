package agent

import (
	"blux/internal/platform"
	"context"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type deepSeekProfile struct {
	Catalog       ModelCatalog
	EntryID       string
	DefaultEffort string
}

func yamlField(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
func yamlString(n *yaml.Node) string {
	if n != nil && n.Kind == yaml.ScalarNode && n.Tag == "!!str" {
		return n.Value
	}
	return ""
}
func parseDeepSeekProfile(raw []byte) (deepSeekProfile, error) {
	p := deepSeekProfile{Catalog: ModelCatalog{Models: []ModelInfo{}, Source: "cli_config"}}
	var root yaml.Node
	if yaml.Unmarshal(raw, &root) != nil {
		return p, errors.New("invalid DeepSeek profile metadata")
	}
	var walk func(*yaml.Node, int)
	walk = func(n *yaml.Node, depth int) {
		if depth > 32 {
			return
		}
		if n.Kind == yaml.MappingNode {
			name := yamlString(yamlField(n, "name"))
			config := yamlField(n, "config")
			if name == "@deepseek-ai/dsh-agent-default-model" {
				p.EntryID = yamlString(yamlField(n, "id"))
				p.DefaultEffort = yamlString(yamlField(config, "reasoningEffort"))
				p.Catalog.DefaultEffort = p.DefaultEffort
				p.Catalog.DefaultModel = yamlString(yamlField(config, "model"))
				p.Catalog.DefaultProvider = yamlString(yamlField(config, "provider"))
			}
			if strings.HasPrefix(name, "@deepseek-ai/dsh-llm-") {
				providers := yamlField(config, "providers")
				if providers != nil && providers.Kind == yaml.MappingNode {
					for i := 0; i+1 < len(providers.Content); i += 2 {
						provider := yamlString(providers.Content[i])
						models := yamlField(providers.Content[i+1], "models")
						if provider == "" || models == nil || models.Kind != yaml.SequenceNode {
							continue
						}
						for _, m := range models.Content {
							model := yamlString(yamlField(m, "id"))
							item := ModelInfo{ID: provider + "/" + model, Model: model, Provider: provider, Name: yamlString(yamlField(m, "name")), Source: "cli_config"}
							efforts := yamlField(m, "reasoningEfforts")
							if efforts != nil && efforts.Kind == yaml.MappingNode {
								for i := 0; i+1 < len(efforts.Content); i += 2 {
									if id := yamlString(efforts.Content[i]); id != "" {
										item.ReasoningEfforts = append(item.ReasoningEfforts, ReasoningOption{ID: id})
									}
								}
							}
							if efforts != nil && efforts.Kind == yaml.SequenceNode {
								for _, effort := range efforts.Content {
									if id := yamlString(yamlField(effort, "id")); id != "" {
										item.ReasoningEfforts = append(item.ReasoningEfforts, ReasoningOption{ID: id})
									}
								}
							}
							p.Catalog.add(item)
						}
					}
				}
			}
		}
		for _, child := range n.Content {
			walk(child, depth+1)
		}
	}
	walk(&root, 0)
	if p.Catalog.DefaultModel != "" && p.Catalog.DefaultProvider != "" {
		p.Catalog.add(ModelInfo{ID: p.Catalog.DefaultProvider + "/" + p.Catalog.DefaultModel, Model: p.Catalog.DefaultModel, Provider: p.Catalog.DefaultProvider, Source: "cli_config"})
	}
	if len(p.Catalog.Models) == 0 {
		p.Catalog.Reason = "CLI 配置未提供可读取的模型目录。"
	}
	return p, nil
}
func readDeepSeekProfile(parent context.Context, binary string) (deepSeekProfile, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--profile", "headless", "--dump-config")
	platform.ConfigureProcess(cmd)
	cmd.Cancel = func() error { return platform.TerminateTree(cmd, true) }
	cmd.WaitDelay = time.Second
	buffer := &codexBoundedBuffer{limit: 4 << 20}
	cmd.Stdout = buffer
	cmd.Stderr = &codexBoundedBuffer{limit: 4096}
	if e := cmd.Run(); e != nil {
		return deepSeekProfile{}, errors.New("cannot read DeepSeek headless model configuration")
	}
	if len(buffer.String()) >= 4<<20 {
		return deepSeekProfile{}, errors.New("DeepSeek model configuration exceeds limit")
	}
	return parseDeepSeekProfile([]byte(buffer.String()))
}
func deepSeekModelPatch(ctx context.Context, binary string, c SessionConfig) (string, string, error) {
	p, e := readDeepSeekProfile(ctx, binary)
	if e != nil {
		return "", "", e
	}
	if p.EntryID == "" {
		return "", "", errors.New("DeepSeek profile has no explicit default model entry")
	}
	provider := c.ModelProvider
	if provider == "" {
		provider = p.Catalog.DefaultProvider
	}
	if provider == "" {
		return "", "", errors.New("DeepSeek model requires a provider")
	}
	model := c.Model
	if model == "" {
		model = p.Catalog.DefaultModel
	}
	if model == "" {
		return "", "", errors.New("DeepSeek model is not configured")
	}
	config := map[string]string{"provider": provider, "model": model}
	if c.ReasoningEffort != "" {
		config["reasoningEffort"] = c.ReasoningEffort
	} else if model == p.Catalog.DefaultModel && provider == p.Catalog.DefaultProvider && p.DefaultEffort != "" {
		config["reasoningEffort"] = p.DefaultEffort
	}
	raw, e := json.Marshal([]any{map[string]any{"id": p.EntryID, "config": config}})
	if e != nil {
		return "", "", e
	}
	path := filepath.Join(c.AgentPath, "model-selection.patch.yml")
	e = os.WriteFile(path, raw, 0600)
	return path, provider, e
}
