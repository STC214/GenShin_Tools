package main

import (
	"os"

	"genshintools/internal/input"
)

func main() {
	if err := input.RunKeyboardWorker(os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
