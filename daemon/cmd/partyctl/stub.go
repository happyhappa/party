package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, _ []string) error {
		fmt.Println("not implemented")
		return errors.New(name + " not implemented")
	}
}
