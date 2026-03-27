package main

import (
	"docksmith/cli"
	"fmt"
	"os"
	"strings"
)

func main() {
	cli.SetupDirectories()

	if len(os.Args) < 2 {
		printUsage()
		return
	}

	switch os.Args[1] {

	case "build":
		// docksmith build [-t <name:tag>] [--no-cache] <context>
		var tag = "image:latest"
		var context = "."
		var noCache = false

		args := os.Args[2:]
		for i := 0; i < len(args); i++ {
			switch args[i] {
			case "-t":
				if i+1 >= len(args) {
					fmt.Println("Error: -t requires a value")
					os.Exit(1)
				}
				tag = args[i+1]
				i++
			case "--no-cache":
				noCache = true
			default:
				context = args[i]
			}
		}
		cli.BuildImage(tag, context, noCache)

	case "images":
		cli.ListImages()

	case "run":
		// docksmith run [-e KEY=VALUE ...] <name:tag> [cmd ...]
		if len(os.Args) < 3 {
			fmt.Println("Usage: docksmith run [-e KEY=VALUE] <name:tag> [cmd...]")
			os.Exit(1)
		}
		var envOverrides []string
		var imageName string
		var cmdOverride []string

		args := os.Args[2:]
		i := 0
		for i < len(args) {
			if args[i] == "-e" {
				if i+1 >= len(args) {
					fmt.Println("Error: -e requires KEY=VALUE")
					os.Exit(1)
				}
				envOverrides = append(envOverrides, args[i+1])
				i += 2
			} else {
				break
			}
		}
		if i >= len(args) {
			fmt.Println("Error: image name required")
			os.Exit(1)
		}
		imageName = args[i]
		i++
		if i < len(args) {
			cmdOverride = args[i:]
		}
		// split cmd if single string was passed
		if len(cmdOverride) == 1 && strings.Contains(cmdOverride[0], " ") {
			cmdOverride = strings.Fields(cmdOverride[0])
		}
		cli.RunContainer(imageName, cmdOverride, envOverrides)

	case "rmi":
		if len(os.Args) < 3 {
			fmt.Println("Usage: docksmith rmi <name:tag>")
			os.Exit(1)
		}
		cli.RemoveImage(os.Args[2])

	default:
		fmt.Println("Unknown command:", os.Args[1])
		printUsage()
	}
}

func printUsage() {
	fmt.Println(`Usage: docksmith <command>

Commands:
  build -t <name:tag> [--no-cache] <context>   Build an image from a Docksmithfile
  images                                        List all local images
  run [-e KEY=VALUE] <name:tag> [cmd...]        Run a container
  rmi <name:tag>                                Remove an image`)
}
