module github.com/norm/relay-daemon

go 1.23.0

require github.com/fsnotify/fsnotify v0.0.0

require (
	github.com/anthropics/anthropic-sdk-go v1.19.0 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
)

replace github.com/fsnotify/fsnotify => ./internal/fsnotify
