module github.com/example/stack-man/agent

go 1.26.0

require (
	github.com/AlecAivazis/survey/v2 v2.3.7
	github.com/BurntSushi/toml v1.6.0
	github.com/example/stack-man/pkg v0.0.0
)

require (
	github.com/kballard/go-shellquote v0.0.0-20180428030007-95032a82bc51 // indirect
	github.com/mattn/go-colorable v0.1.2 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mgutz/ansi v0.0.0-20170206155736-9520e82c474b // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/term v0.47.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/example/stack-man/pkg => ../pkg
