package cmd

import (
	"fmt"
	"os"

	"golang.org/x/term"
)

// Interactive startup never claims a terminal from /dev/tty: both command
// streams must be attached. This keeps pipelines and redirected reports stable.
func isTerminal(stream interface{}) bool {
	file, ok := stream.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func inspectInteractiveMode(requested, explicit, outputExplicit, inputTTY, outputTTY bool, terminal string) (bool, error) {
	if explicit && requested {
		if outputExplicit {
			return false, fmt.Errorf("--interactive and --output select different modes; choose one")
		}
		if !inputTTY || !outputTTY || terminal == "dumb" {
			return false, fmt.Errorf("interactive inspection requires terminal input and output; use --output table, json, or yaml for a report")
		}
		return true, nil
	}
	if explicit || outputExplicit {
		return false, nil
	}
	return inputTTY && outputTTY && terminal != "dumb", nil
}
