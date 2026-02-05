package main

import (
	"github.com/sklarsa/incus-azure-pipelines/cmd"
)

const (
	defaultMetricsPort = 9922
)

var CLI struct {
	Run struct {
	}
	Provision struct {
	}
	Logs struct {
	}
}

func main() {
	cmd.Execute()
}
