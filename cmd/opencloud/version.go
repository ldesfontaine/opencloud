package main

import (
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/version"
)

func runVersion() error {
	_, err := fmt.Println("opencloud", version.String())
	return err
}
