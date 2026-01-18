package main

import (
	"flag"
	"fmt"
	"os"
)

type Config struct {
	Repo   string
	Root   string
	Max    int
	DryRun bool
	Model  string
	Agent  string
}

func parseArgs(args []string) (*Config, error) {
	config := &Config{
		Repo:   ".",
		Root:   "algi-8bt",
		Max:    0,
		DryRun: false,
		Model:  "",
		Agent:  "yolo",
	}

	fs := flag.NewFlagSet("yolo-runner", flag.ContinueOnError)

	repoFlag := fs.String("repo", ".", "repository path")
	rootFlag := fs.String("root", "algi-8bt", "root directory")
	maxFlag := fs.Int("max", 0, "maximum iterations")
	dryRunFlag := fs.Bool("dry-run", false, "dry run mode")
	modelFlag := fs.String("model", "", "opencode model")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	config.Repo = *repoFlag
	config.Root = *rootFlag
	config.Max = *maxFlag
	config.DryRun = *dryRunFlag
	config.Model = *modelFlag

	return config, nil
}

func main() {
	fs := flag.NewFlagSet("yolo-runner", flag.ExitOnError)
	fs.String("repo", ".", "repository path")
	fs.String("root", "algi-8bt", "root directory")
	fs.Int("max", 0, "maximum iterations")
	fs.Bool("dry-run", false, "dry run mode")
	fs.String("model", "", "opencode model")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: yolo-runner [options]\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fmt.Fprintf(os.Stderr, "  --repo string\n    repository path (default \".\")\n")
		fmt.Fprintf(os.Stderr, "  --root string\n    root directory (default \"algi-8bt\")\n")
		fmt.Fprintf(os.Stderr, "  --max int\n    maximum iterations\n")
		fmt.Fprintf(os.Stderr, "  --dry-run\n    dry run mode\n")
		fmt.Fprintf(os.Stderr, "  --model string\n    opencode model\n")
	}

	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			fs.Usage()
			os.Exit(0)
		}
	}

	config, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	_ = config
}
