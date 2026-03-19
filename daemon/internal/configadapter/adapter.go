package configadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/norm/relay-daemon/internal/contract"
)

// MutationResult describes the effect of a single config mutation.
type MutationResult struct {
	Path     string      `json:"path"`
	Action   string      `json:"action"`
	OldValue interface{} `json:"old_value,omitempty"`
	NewValue interface{} `json:"new_value,omitempty"`
	Changed  bool        `json:"changed"`
}

// ConfigAdapter edits one structured config file format.
type ConfigAdapter interface {
	Load(path string) error
	Get(keyPath string) (interface{}, error)
	Apply(mutations []contract.ConfigMutationSpec) error
	DryRun(mutations []contract.ConfigMutationSpec) ([]MutationResult, error)
	Write(path string) error
}

// LoadFile builds an adapter, loads the target file, and applies the
// required-vs-optional missing-file policy from the contract.
func LoadFile(spec contract.ConfigFileSpec) (ConfigAdapter, error) {
	adapter, err := NewAdapter(spec.Format)
	if err != nil {
		return nil, err
	}
	if err := adapter.Load(spec.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) && !spec.Required {
			return adapter, nil
		}
		if errors.Is(err, os.ErrNotExist) && spec.Required {
			return nil, fmt.Errorf("required config file missing: %s", spec.Path)
		}
		return nil, err
	}
	return adapter, nil
}

type formatCodec interface {
	parse(data []byte) (map[string]interface{}, error)
	encode(doc map[string]interface{}) ([]byte, error)
}

type baseAdapter struct {
	codec         formatCodec
	doc           map[string]interface{}
	loadedPath    string
	loaded        bool
	existed       bool
	mode          os.FileMode
	originalBytes []byte
	backupOnApply bool
	backupWritten bool
}

func (a *baseAdapter) Load(path string) error {
	a.loadedPath = path
	a.loaded = true
	a.backupWritten = false
	a.doc = map[string]interface{}{}
	a.originalBytes = nil
	a.mode = 0o644
	a.existed = false

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return err
		}
		return fmt.Errorf("read config %q: %w", path, err)
	}

	info, err := os.Stat(path)
	if err == nil {
		a.mode = info.Mode().Perm()
	}
	doc, err := a.codec.parse(data)
	if err != nil {
		return fmt.Errorf("parse config %q: %w", path, err)
	}
	a.doc = doc
	a.existed = true
	a.originalBytes = append([]byte(nil), data...)
	return nil
}

func (a *baseAdapter) Get(keyPath string) (interface{}, error) {
	if !a.loaded {
		return nil, fmt.Errorf("config not loaded")
	}
	value, ok, err := lookupPath(a.doc, splitPath(keyPath))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("path %q not found", keyPath)
	}
	return outputValue(value), nil
}

func (a *baseAdapter) Apply(mutations []contract.ConfigMutationSpec) error {
	if !a.loaded {
		return fmt.Errorf("config not loaded")
	}
	if a.backupOnApply && a.existed && !a.backupWritten {
		if err := writeBackup(a.loadedPath+".bak", a.originalBytes, a.mode); err != nil {
			return err
		}
		a.backupWritten = true
	}
	_, err := applyMutations(a.doc, mutations)
	return err
}

func (a *baseAdapter) DryRun(mutations []contract.ConfigMutationSpec) ([]MutationResult, error) {
	if !a.loaded {
		return nil, fmt.Errorf("config not loaded")
	}
	clone, err := cloneDocument(a.doc)
	if err != nil {
		return nil, err
	}
	return applyMutations(clone, mutations)
}

func (a *baseAdapter) Write(path string) error {
	if !a.loaded {
		return fmt.Errorf("config not loaded")
	}
	if path == "" {
		return fmt.Errorf("write path is required")
	}
	data, err := a.codec.encode(a.doc)
	if err != nil {
		return fmt.Errorf("encode config %q: %w", path, err)
	}
	if err := atomicWrite(path, data, a.mode); err != nil {
		return err
	}
	a.loadedPath = path
	a.existed = true
	a.originalBytes = append([]byte(nil), data...)
	return nil
}

func applyMutations(doc map[string]interface{}, mutations []contract.ConfigMutationSpec) ([]MutationResult, error) {
	results := make([]MutationResult, 0, len(mutations))
	for _, mutation := range mutations {
		result, err := applyMutation(doc, mutation)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func applyMutation(doc map[string]interface{}, mutation contract.ConfigMutationSpec) (MutationResult, error) {
	segments := splitPath(mutation.Path)
	if len(segments) == 0 {
		return MutationResult{}, fmt.Errorf("mutation path is empty")
	}

	parent, err := ensureParentMap(doc, segments[:len(segments)-1], mutation.CreateParents)
	if err != nil {
		return MutationResult{}, fmt.Errorf("prepare path %q: %w", mutation.Path, err)
	}

	leaf := segments[len(segments)-1]
	oldValue, exists := parent[leaf]
	result := MutationResult{
		Path:     mutation.Path,
		Action:   mutation.Action,
		OldValue: cloneValue(oldValue),
	}

	switch mutation.Action {
	case "replace":
		newValue, err := mutationValue(mutation)
		if err != nil {
			return MutationResult{}, err
		}
		parent[leaf] = cloneValue(newValue)
		result.NewValue = cloneValue(newValue)
		result.Changed = !exists || !reflect.DeepEqual(normalizeValue(oldValue), normalizeValue(newValue))
	case "ensure_exists":
		if exists {
			result.NewValue = cloneValue(oldValue)
			result.Changed = false
			return result, nil
		}
		newValue, err := mutationValue(mutation)
		if err != nil {
			return MutationResult{}, err
		}
		parent[leaf] = cloneValue(newValue)
		result.NewValue = cloneValue(newValue)
		result.Changed = true
	case "ensure_table":
		if exists {
			table, ok := toMap(oldValue)
			if !ok {
				return MutationResult{}, fmt.Errorf("path %q exists but is not a table", mutation.Path)
			}
			result.OldValue = cloneValue(table)
			result.NewValue = cloneValue(table)
			result.Changed = false
			return result, nil
		}
		parent[leaf] = map[string]interface{}{}
		result.NewValue = map[string]interface{}{}
		result.Changed = true
	case "merge_union":
		additions, err := toStringSliceFromMutation(mutation)
		if err != nil {
			return MutationResult{}, err
		}
		current := []string{}
		if exists {
			current, err = toStringSlice(oldValue)
			if err != nil {
				return MutationResult{}, fmt.Errorf("path %q: %w", mutation.Path, err)
			}
		}
		merged := mergeUnion(current, additions)
		parent[leaf] = cloneStringSlice(merged)
		result.OldValue = cloneStringSlice(current)
		result.NewValue = cloneStringSlice(merged)
		result.Changed = !reflect.DeepEqual(current, merged)
	default:
		return MutationResult{}, fmt.Errorf("unsupported mutation action %q", mutation.Action)
	}

	return result, nil
}

func mutationValue(mutation contract.ConfigMutationSpec) (interface{}, error) {
	switch mutation.ValueType {
	case "string":
		return mutation.StringValue, nil
	case "bool":
		if mutation.BoolValue == nil {
			return nil, fmt.Errorf("bool mutation %q missing bool_value", mutation.Path)
		}
		return *mutation.BoolValue, nil
	case "string_array":
		return cloneStringSlice(uniqueStrings(mutation.ArrayValue)), nil
	case "object":
		out := make(map[string]interface{}, len(mutation.ObjectValue))
		for key, value := range mutation.ObjectValue {
			out[key] = value
		}
		return out, nil
	case "":
		return nil, fmt.Errorf("mutation %q missing value_type", mutation.Path)
	default:
		return nil, fmt.Errorf("mutation %q has unsupported value_type %q", mutation.Path, mutation.ValueType)
	}
}

func ensureParentMap(root map[string]interface{}, segments []string, createParents bool) (map[string]interface{}, error) {
	current := root
	for _, segment := range segments {
		next, exists := current[segment]
		if !exists {
			if !createParents {
				return nil, fmt.Errorf("parent table %q missing", segment)
			}
			child := map[string]interface{}{}
			current[segment] = child
			current = child
			continue
		}
		child, ok := toMap(next)
		if !ok {
			return nil, fmt.Errorf("parent path %q is not a table", segment)
		}
		current[segment] = child
		current = child
	}
	return current, nil
}

func lookupPath(root map[string]interface{}, segments []string) (interface{}, bool, error) {
	if len(segments) == 0 {
		return cloneValue(root), true, nil
	}
	current := interface{}(root)
	for _, segment := range segments {
		table, ok := toMap(current)
		if !ok {
			return nil, false, fmt.Errorf("path segment %q is not a table", segment)
		}
		next, exists := table[segment]
		if !exists {
			return nil, false, nil
		}
		current = next
	}
	return current, true, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir for %q: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".partyctl-*")
	if err != nil {
		return fmt.Errorf("create temp file for %q: %w", path, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file for %q: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file for %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file for %q: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp file for %q: %w", path, err)
	}
	return nil
}

func writeBackup(path string, data []byte, mode os.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := atomicWrite(path, data, mode); err != nil {
		return fmt.Errorf("write backup %q: %w", path, err)
	}
	return nil
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cloneDocument(doc map[string]interface{}) (map[string]interface{}, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("clone document: %w", err)
	}
	var cloned map[string]interface{}
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, fmt.Errorf("clone document: %w", err)
	}
	if cloned == nil {
		cloned = map[string]interface{}{}
	}
	return cloned, nil
}

func cloneValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		cloned := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			cloned[key] = cloneValue(item)
		}
		return cloned
	case []interface{}:
		cloned := make([]interface{}, len(typed))
		for i, item := range typed {
			cloned[i] = cloneValue(item)
		}
		return cloned
	case []string:
		return cloneStringSlice(typed)
	default:
		return typed
	}
}

func outputValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = outputValue(item)
		}
		return out
	case []interface{}:
		stringsOnly := make([]string, 0, len(typed))
		for _, item := range typed {
			str, ok := item.(string)
			if !ok {
				out := make([]interface{}, len(typed))
				for i, nested := range typed {
					out[i] = outputValue(nested)
				}
				return out
			}
			stringsOnly = append(stringsOnly, str)
		}
		return stringsOnly
	case []string:
		return cloneStringSlice(typed)
	default:
		return typed
	}
}

func cloneStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func normalizeValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case []interface{}:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = normalizeValue(item)
		}
		return out
	case []string:
		out := make([]interface{}, len(typed))
		for i, item := range typed {
			out[i] = item
		}
		return out
	case map[string]interface{}:
		out := make(map[string]interface{}, len(typed))
		for key, item := range typed {
			out[key] = normalizeValue(item)
		}
		return out
	default:
		return typed
	}
}

func mergeUnion(existing, additions []string) []string {
	out := make([]string, 0, len(existing)+len(additions))
	seen := map[string]bool{}
	for _, value := range existing {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	for _, value := range additions {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func uniqueStrings(values []string) []string {
	return mergeUnion(nil, values)
}

func toStringSlice(value interface{}) ([]string, error) {
	switch typed := value.(type) {
	case []string:
		return cloneStringSlice(typed), nil
	case []interface{}:
		out := make([]string, len(typed))
		for i, item := range typed {
			str, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected string array, got %T", item)
			}
			out[i] = str
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected string array, got %T", value)
	}
}

func toStringSliceFromMutation(mutation contract.ConfigMutationSpec) ([]string, error) {
	if mutation.ValueType != "string_array" {
		return nil, fmt.Errorf("mutation %q requires value_type string_array", mutation.Path)
	}
	return uniqueStrings(mutation.ArrayValue), nil
}

func toMap(value interface{}) (map[string]interface{}, bool) {
	if value == nil {
		return nil, false
	}
	table, ok := value.(map[string]interface{})
	return table, ok
}
