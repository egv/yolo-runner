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
	os.Exit(RunMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func RunMain(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	fs := flag.NewFlagSet("yolo-tui", flag.ContinueOnError)
	fs.SetOutput(errOut)
	eventsStdin := fs.Bool("events-stdin", true, "Read NDJSON events from stdin")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if !*eventsStdin {
		fmt.Fprintln(errOut, "--events-stdin must be enabled")
		return 1
	}
	if in == nil {
		fmt.Fprintln(errOut, "stdin reader is required")
		return 1
	}

	if err := renderFromReader(in, out, errOut); err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	return 0
}

func renderFromReader(reader io.Reader, out io.Writer, errOut io.Writer) error {
	decoder := contracts.NewEventDecoder(reader)
	model := monitor.NewModel(nil)
	haveEvents := false
	for {
		event, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		haveEvents = true
		model.Apply(event)
		if _, writeErr := io.WriteString(out, model.View()); writeErr != nil {
			return writeErr
		}
	}
	if !haveEvents {
		if _, writeErr := io.WriteString(out, model.View()); writeErr != nil {
			return writeErr
		}
	}
	_ = errOut
	return nil
}
