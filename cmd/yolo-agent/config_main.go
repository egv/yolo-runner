package main

import (
	"fmt"
	"os"
)

var runConfigValidateMain = func(args []string) int {
	fmt.Fprintln(os.Stderr, "yolo-agent config validate is not implemented yet")
	return 1
}

var runConfigInitMain = func(args []string) int {
	fmt.Fprintln(os.Stderr, "yolo-agent config init is not implemented yet")
	return 1
}

func RunConfigMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: yolo-agent config <validate|init> [flags]")
		return 1
	}

	switch args[0] {
	case "validate":
		return runConfigValidateMain(args[1:])
	case "init":
		return runConfigInitMain(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown config command: %s\n", args[0])
		fmt.Fprintln(os.Stderr, "usage: yolo-agent config <validate|init> [flags]")
		return 1
	}
}
