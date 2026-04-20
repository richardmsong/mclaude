module mclaude-cli

go 1.25.0

require (
	github.com/rs/zerolog v1.35.0
	mclaude.io/common v0.0.0
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
)

replace mclaude.io/common => ../mclaude-common
