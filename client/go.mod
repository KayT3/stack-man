module github.com/example/stack-man/client

go 1.26.0

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/example/stack-man/pkg v0.0.0
	golang.org/x/crypto v0.58.0
)

require golang.org/x/sys v0.49.0 // indirect

replace github.com/example/stack-man/pkg => ../pkg
