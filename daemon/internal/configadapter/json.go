package configadapter

import "fmt"

type jsonCodec struct{}

func NewAdapter(format string) (ConfigAdapter, error) {
	switch format {
	case "json":
		return &baseAdapter{codec: jsonCodec{}}, nil
	case "toml":
		return newTOMLAdapter(), nil
	default:
		return nil, fmt.Errorf("unsupported config format %q", format)
	}
}
