package main

import (
	"context"
	"log"

	"github.com/XferOps/system1/internal/cli"
)

func main() {
	if err := cli.Execute(context.Background()); err != nil {
		log.Fatal(err)
	}
}
