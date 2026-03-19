package configadapter

import (
	"encoding/json"

	toml "github.com/pelletier/go-toml/v2"
)

func (jsonCodec) parse(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	return doc, nil
}

func (jsonCodec) encode(doc map[string]interface{}) ([]byte, error) {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

type tomlCodec struct{}

func newTOMLAdapter() ConfigAdapter {
	return &baseAdapter{
		codec:         tomlCodec{},
		backupOnApply: true,
	}
}

func (tomlCodec) parse(data []byte) (map[string]interface{}, error) {
	if len(data) == 0 {
		return map[string]interface{}{}, nil
	}
	var doc map[string]interface{}
	if err := toml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if doc == nil {
		doc = map[string]interface{}{}
	}
	return doc, nil
}

func (tomlCodec) encode(doc map[string]interface{}) ([]byte, error) {
	return toml.Marshal(doc)
}
