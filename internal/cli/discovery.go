package cli

import (
	"fmt"
	"io"
	"runtime/debug"
)

// buildVersion is set by release builds through -ldflags -X.
var buildVersion = "development"

func runVersion(args []string, out, errout io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, "Usage: twi version")
		return 0
	}
	if len(args) > 0 {
		fmt.Fprintln(errout, "version takes no arguments")
		return 2
	}
	version := buildVersion
	revision := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		if (version == "development" || version == "") && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
		}
	}
	if _, err := fmt.Fprintf(out, "twi %s (%s)\n", version, revision); err != nil {
		return 7
	}
	return 0
}
func runCompletion(args []string, out, errout io.Writer) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, "Usage: twi completion bash|zsh|fish")
		return 0
	}
	if len(args) != 1 {
		fmt.Fprintln(errout, "usage: twi completion bash|zsh|fish")
		return 2
	}
	commands := "chat produce consume record history account config doctor login profile setup version completion"
	var script string
	switch args[0] {
	case "bash":
		script = "complete -W '" + commands + "' twi\n"
	case "zsh":
		script = "#compdef twi\n_arguments '1:command:(" + commands + ")'\n"
	case "fish":
		script = "complete -c twi -f -a '" + commands + "'\n"
	default:
		fmt.Fprintln(errout, "unsupported completion shell")
		return 2
	}
	if _, err := io.WriteString(out, script); err != nil {
		return 7
	}
	return 0
}
