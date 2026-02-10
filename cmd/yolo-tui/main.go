package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/anomalyco/yolo-runner/internal/contracts"
	"github.com/anomalyco/yolo-runner/internal/ui/monitor"
)

func main() {
	os.Exit(RunMain(os.Args[1:], os.Stdout, os.Stderr))
}

func RunMain(args []string, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("yolo-tui", flag.ContinueOnError)
	fs.SetOutput(errOut)
	events := fs.String("events", "", "Path to JSONL events file")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *events == "" {
		fmt.Fprintln(errOut, "--events is required")
		return 1
	}

	file, err := os.Open(*events)
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	defer file.Close()

	if err := renderFromReader(file, out, errOut); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func renderFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	decoder := contracts.NewEventDecoder(reader)
	model := monitor.NewModel(nil)
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		model.Apply(event)
	}
	_, writeErr := io.WriteString(out, model.View())
	if writeErr != nil {
		return writeErr
	}
	_ = errOut
	return nil
}
