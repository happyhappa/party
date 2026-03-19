module github.com/norm/relay-daemon

go 1.23.0

require github.com/fsnotify/fsnotify v0.0.0

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)

require (
	github.com/anthropics/anthropic-sdk-go v1.19.0
	github.com/pelletier/go-toml/v2 v2.2.4
	github.com/spf13/cobra v1.10.2
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
)

replace github.com/fsnotify/fsnotify => ./internal/fsnotify
replace github.com/pelletier/go-toml/v2 => /home/phileas/go/pkg/mod/github.com/pelletier/go-toml/v2@v2.2.4
