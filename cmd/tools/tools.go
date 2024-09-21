//go:build tools
// +build tools

// this file exists to manage tools via go modules

package main

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
)
