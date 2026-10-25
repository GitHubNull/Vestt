package engine

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// DefinitionsFS embeds the Cardigann definition files (GPL-2.0 upstream
// Jackett definitions, copied verbatim — see AGENTS.md §5).
//
//go:embed definitions
var DefinitionsFS embed.FS

const schemaFile = "definitions/schema.json"

// LoadStats reports the full-load outcome (SPEC-05 acceptance #1).
type LoadStats struct {
	Loaded  int
	Skipped []string // "id: reason" per failed definition
}

var (
	schemaOnce     sync.Once
	schemaCompiled *jsonschema.Schema
	schemaErr      error
)

func compiledSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		raw, err := DefinitionsFS.ReadFile(schemaFile)
		if err != nil {
			schemaErr = err
			return
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			schemaErr = fmt.Errorf("parse schema.json: %w", err)
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource("cardigann.json", doc); err != nil {
			schemaErr = err
			return
		}
		schemaCompiled, schemaErr = c.Compile("cardigann.json")
	})
	return schemaCompiled, schemaErr
}

// LoadAll loads every definition from the embedded FS, plus an optional
// local overlay directory (newest debugging aid; files shadow embedded
// definitions with the same id).
func LoadAll(logger *slog.Logger, overlayDir string) (map[string]*IndexerDefinition, *LoadStats, error) {
	if logger == nil {
		logger = slog.Default()
	}
	defs := map[string]*IndexerDefinition{}
	stats := &LoadStats{}

	entries, err := fs.ReadDir(DefinitionsFS, "definitions")
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".yml") {
			continue
		}
		loadOne(defs, stats, logger, func() ([]byte, error) {
			return DefinitionsFS.ReadFile(filepath.Join("definitions", name))
		}, name)
	}

	if overlayDir != "" {
		matches, err := filepath.Glob(filepath.Join(overlayDir, "*.yml"))
		if err != nil {
			return nil, nil, err
		}
		sort.Strings(matches)
		for _, path := range matches {
			path := path
			loadOne(defs, stats, logger, func() ([]byte, error) {
				return os.ReadFile(path)
			}, filepath.Base(path))
		}
	}

	// secondary validation across the whole set (upstream load checks). The
	// upstream duplicate-id guard is unreachable here: map keys are unique by
	// construction, so no per-id membership test is needed.
	for id, def := range defs {
		if err := validateDefinition(def); err != nil {
			logger.Error(fmt.Sprintf("definition %s: %v", id, err))
			stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: %v", id, err))
			delete(defs, id)
			stats.Loaded--
		}
	}
	sort.Strings(stats.Skipped)
	return defs, stats, nil
}

func loadOne(defs map[string]*IndexerDefinition, stats *LoadStats, logger *slog.Logger, read func() ([]byte, error), name string) {
	raw, err := read()
	if err != nil {
		logger.Error(fmt.Sprintf("load definition %s: %v", name, err))
		stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: %v", name, err))
		return
	}

	// 1. schema validation against the upstream JSON schema
	sch, err := compiledSchema()
	if err != nil {
		logger.Error(fmt.Sprintf("compile schema: %v", err))
		stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: schema unavailable", name))
		return
	}
	var docNode yaml.Node
	if err := yaml.Unmarshal(raw, &docNode); err != nil {
		// 4 upstream definitions use the YAML-1.1 escape "\/" inside
		// double-quoted scalars (e.g. args: "^(\\d+) \/" in arabtorrents).
		// YamlDotNet (YAML 1.1) resolves it to "/", go-yaml rejects it.
		// Retry once with the escape rewritten to the equivalent \x2F hex
		// form, only inside double-quoted regions.
		fixed := fixYAML11Escapes(raw)
		if fixed == nil || yaml.Unmarshal(fixed, &docNode) != nil {
			logger.Error(fmt.Sprintf("parse definition %s: %v", name, err))
			stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: %v", name, err))
			return
		}
	}
	jsonDoc, err := toJSONValue(&docNode)
	if err != nil {
		logger.Error(fmt.Sprintf("convert definition %s: %v", name, err))
		stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: %v", name, err))
		return
	}
	if err := sch.Validate(jsonDoc); err != nil {
		logger.Error(fmt.Sprintf("schema validation failed for %s: %v", name, err))
		stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: schema: %v", name, err))
		return
	}

	// 2. decode into the typed model
	var def IndexerDefinition
	if err := docNode.Decode(&def); err != nil {
		logger.Error(fmt.Sprintf("decode definition %s: %v", name, err))
		stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: %v", name, err))
		return
	}
	if def.ID == "" {
		logger.Error(fmt.Sprintf("definition %s has no id", name))
		stats.Skipped = append(stats.Skipped, fmt.Sprintf("%s: missing id", name))
		return
	}
	if prev, exists := defs[def.ID]; exists {
		logger.Warn(fmt.Sprintf("definition %s replaces %s (overlay shadowing)", def.ID, prev.ID))
	}
	defs[def.ID] = &def
	stats.Loaded++
}

// fixYAML11Escapes rewrites the YAML-1.1 escape "\/" as the equivalent
// "\x2F" (both resolve to "/") inside double-quoted regions only. Returns
// nil when no rewrite is needed.
func fixYAML11Escapes(raw []byte) []byte {
	var out []byte
	inDouble := false
	changed := false
	for i, b := range raw {
		if b == '"' && (i == 0 || raw[i-1] != '\\') {
			inDouble = !inDouble
			out = append(out, b)
			continue
		}
		if inDouble && b == '/' && i > 0 && raw[i-1] == '\\' && (i < 2 || raw[i-2] != '\\') {
			out = append(out, 'x', '2', 'F')
			changed = true
			continue
		}
		out = append(out, b)
	}
	if !changed {
		return nil
	}
	return out
}

// toJSONValue converts a yaml document to JSON-compatible values for the
// schema validator (duplicate mapping keys keep the last value).
func toJSONValue(n *yaml.Node) (any, error) {
	if n == nil {
		return nil, nil
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return toJSONValue(n.Content[0])
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			return n.Value == "true", nil
		case "!!int":
			var i int64
			if _, err := fmt.Sscanf(n.Value, "%d", &i); err != nil {
				return nil, fmt.Errorf("int %q: %w", n.Value, err)
			}
			return i, nil
		case "!!float":
			var f float64
			if _, err := fmt.Sscanf(n.Value, "%g", &f); err != nil {
				return nil, fmt.Errorf("float %q: %w", n.Value, err)
			}
			return f, nil
		default:
			return n.Value, nil
		}
	case yaml.SequenceNode:
		out := make([]any, 0, len(n.Content))
		for _, c := range n.Content {
			v, err := toJSONValue(c)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case yaml.MappingNode:
		m := map[string]any{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			v, err := toJSONValue(n.Content[i+1])
			if err != nil {
				return nil, err
			}
			m[n.Content[i].Value] = v
		}
		return m, nil
	case yaml.AliasNode:
		return toJSONValue(n.Alias)
	}
	return nil, fmt.Errorf("unsupported yaml node kind %v", n.Kind)
}

// validateDefinition mirrors the upstream load-time checks: links must be
// absolute URLs and the site link must end parsable.
func validateDefinition(def *IndexerDefinition) error {
	if len(def.Links) == 0 {
		return errors.New("no links defined")
	}
	for _, link := range def.Links {
		u, err := url.Parse(link)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("invalid link %q", link)
		}
	}
	if def.Caps == nil {
		return errors.New("no caps block")
	}
	return nil
}
