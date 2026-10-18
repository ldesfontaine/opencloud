package main

import (
	"fmt"

	"github.com/ldesfontaine/opencloud/internal/brand"
	"github.com/ldesfontaine/opencloud/internal/version"
)

func runVersion() error {
	_, err := fmt.Println(brand.Command, version.String())
	return err
}
