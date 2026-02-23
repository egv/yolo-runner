package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/egv/yolo-runner/internal/contracts"
	"github.com/egv/yolo-runner/internal/tk"
)

func main() {
	os.Exit(RunMain(os.Args[1:], os.Stdout, os.Stderr, tk.NewTaskManager(localRunner{})))
}

func RunMain(args []string, out io.Writer, errOut io.Writer, manager contracts.TaskManager) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, "usage: yolo-task <next|status|data> [flags]")
		return 1
	}

	sub := args[0]
	switch sub {
	case "next":
		return runNext(args[1:], out, errOut, manager)
	case "status":
		return runStatus(args[1:], errOut, manager)
	case "data":
		return runData(args[1:], errOut, manager)
	default:
		fmt.Fprintf(errOut, "unknown command: %s\n", sub)
		return 1
	}
}

func runNext(args []string, out io.Writer, errOut io.Writer, manager contracts.TaskManager) int {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	fs.SetOutput(errOut)
	root := fs.String("root", "", "Root task ID")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *root == "" {
		fmt.Fprintln(errOut, "--root is required")
		return 1
	}

	tasks, err := manager.NextTasks(context.Background(), *root)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	for _, task := range tasks {
		fmt.Fprintln(out, task.ID)
	}
	return 0
}

func runStatus(args []string, errOut io.Writer, manager contracts.TaskManager) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(errOut)
	id := fs.String("id", "", "Task ID")
	status := fs.String("status", "", "Task status")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *id == "" || *status == "" {
		fmt.Fprintln(errOut, "--id and --status are required")
		return 1
	}
	if err := manager.SetTaskStatus(context.Background(), *id, contracts.TaskStatus(*status)); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

type arrayFlags []string

func (a *arrayFlags) String() string { return strings.Join(*a, ",") }
func (a *arrayFlags) Set(value string) error {
	*a = append(*a, value)
	return nil
}

func runData(args []string, errOut io.Writer, manager contracts.TaskManager) int {
	fs := flag.NewFlagSet("data", flag.ContinueOnError)
	fs.SetOutput(errOut)
	id := fs.String("id", "", "Task ID")
	var entries arrayFlags
	fs.Var(&entries, "set", "Key=value pair")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *id == "" {
		fmt.Fprintln(errOut, "--id is required")
		return 1
	}
	data := map[string]string{}
	for _, entry := range entries {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(errOut, "invalid --set value: %s\n", entry)
			return 1
		}
		data[parts[0]] = parts[1]
	}
	if err := manager.SetTaskData(context.Background(), *id, data); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

type localRunner struct{}

func (localRunner) Run(args ...string) (string, error) {
	cmd := exec.Command(args[0], args[1:]...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
