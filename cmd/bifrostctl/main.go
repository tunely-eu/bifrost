package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/tunely-eu/bifrost/internal/config"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println(version)
	case "config":
		runConfig(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func runConfig(args []string) {
	if len(args) < 1 {
		configUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "example":
		fs := flag.NewFlagSet("bifrostctl config example", flag.ExitOnError)
		server := fs.Bool("server", false, "print server example")
		client := fs.Bool("client", false, "print client example")
		_ = fs.Parse(args[1:])
		switch {
		case *server == *client:
			fmt.Fprintln(os.Stderr, "choose exactly one of --server or --client")
			os.Exit(2)
		case *server:
			fmt.Print(config.ExampleServerYAML)
		case *client:
			fmt.Print(config.ExampleClientYAML)
		}
	case "validate":
		fs := flag.NewFlagSet("bifrostctl config validate", flag.ExitOnError)
		server := fs.Bool("server", false, "validate server config")
		client := fs.Bool("client", false, "validate client config")
		file := fs.String("file", "", "config file")
		_ = fs.Parse(args[1:])
		if *file == "" {
			fmt.Fprintln(os.Stderr, "--file is required")
			os.Exit(2)
		}
		if *server == *client {
			fmt.Fprintln(os.Stderr, "choose exactly one of --server or --client")
			os.Exit(2)
		}
		var err error
		if *server {
			_, err = config.LoadServerFile(*file)
		} else {
			_, err = config.LoadClientFile(*file)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "invalid: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("valid")
	default:
		configUsage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bifrostctl <version|config>")
}

func configUsage() {
	fmt.Fprintln(os.Stderr, "usage: bifrostctl config <example|validate>")
}
