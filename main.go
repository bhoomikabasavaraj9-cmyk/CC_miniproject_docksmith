package main

import (
	"docksmith/cli"
	"fmt"
	"os"
)

func main() {

	// create ~/.docksmith directories
	cli.SetupDirectories()

	if len(os.Args) < 2 {
		fmt.Println("Usage: docksmith <command>")
		return
	}

	command := os.Args[1]

	switch command {

	case "build":

		if len(os.Args) < 5 {
			fmt.Println("Usage: docksmith build -t <name:tag> <context>")
			return
		}

		tag := os.Args[3]
		context := os.Args[4]

		cli.BuildImage(tag, context)

	case "images":

		cli.ListImages()

	case "run":

		if len(os.Args) < 3 {
			fmt.Println("Usage: docksmith run <name:tag>")
			return
		}

		image := os.Args[2]

		cli.RunContainer(image)

	case "rmi":

		if len(os.Args) < 3 {
			fmt.Println("Usage: docksmith rmi <name:tag>")
			return
		}

		image := os.Args[2]

		cli.RemoveImage(image)

	default:

		fmt.Println("Unknown command:", command)
	}
}
