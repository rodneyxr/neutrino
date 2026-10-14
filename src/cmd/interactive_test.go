package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestInspectInteractiveSelection(t *testing.T) {
	for _, tt := range []struct {
		name                                       string
		requested, explicit, output, inTTY, outTTY bool
		term                                       string
		want, wantError                            bool
	}{
		{name: "attached terminal", inTTY: true, outTTY: true, term: "xterm", want: true},
		{name: "stdout pipe", inTTY: true, term: "xterm"},
		{name: "stdin pipe", outTTY: true, term: "xterm"},
		{name: "redirected streams", term: "xterm"},
		{name: "dumb terminal", inTTY: true, outTTY: true, term: "dumb"},
		{name: "explicit report", output: true, inTTY: true, outTTY: true, term: "xterm"},
		{name: "explicit false", explicit: true, inTTY: true, outTTY: true, term: "xterm"},
		{name: "forced terminal", requested: true, explicit: true, inTTY: true, outTTY: true, term: "xterm", want: true},
		{name: "force into pipe", requested: true, explicit: true, inTTY: true, term: "xterm", wantError: true},
		{name: "force missing input", requested: true, explicit: true, outTTY: true, term: "xterm", wantError: true},
		{name: "conflicting flags", requested: true, explicit: true, output: true, inTTY: true, outTTY: true, term: "xterm", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := inspectInteractiveMode(tt.requested, tt.explicit, tt.output, tt.inTTY, tt.outTTY, tt.term)
			if got != tt.want || (err != nil) != tt.wantError {
				t.Fatalf("mode=%v err=%v, want mode=%v error=%v", got, err, tt.want, tt.wantError)
			}
		})
	}
}

func TestInspectRejectsUnusableModeBeforeReadingSource(t *testing.T) {
	for _, tt := range []struct{ flag, want string }{
		{"--interactive", "requires terminal"},
		{"--output=invalid", "unsupported output format"},
	} {
		var output bytes.Buffer
		command := newInspectCmd(&output)
		command.SetIn(strings.NewReader(""))
		command.SetOut(&output)
		command.SetErr(&output)
		command.SetArgs([]string{"/definitely-not-a-package", tt.flag})
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%s: got %v", tt.flag, err)
		}
	}
}

func TestInspectHelpExplainsOptionalConfiguration(t *testing.T) {
	var output bytes.Buffer
	command := newInspectCmd(&output)
	command.SetOut(&output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--config", "Optional config.uds.hcl", "on by default in the TUI"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("help is missing %q:\n%s", want, output.String())
		}
	}
}
