package cmd

import "testing"

// A failing command must print its message ONCE.
//
// It printed twice for as long as the CLI has existed: Cobra printed the error
// AND main() printed it again, because main is where the outdated-binary hint
// gets appended. Two identical lines read as two separate problems, and an
// agent parsing stderr counts a phantom failure.
//
// The fix is one flag, which is exactly why it needs a test — nothing else in
// the tree would notice it being dropped.
func TestRootSilencesCobrasOwnErrorPrinting(t *testing.T) {
	root := NewRootCmd("test")
	if !root.SilenceErrors {
		t.Fatal("root must set SilenceErrors: main() is the single place that " +
			"prints a command error (and appends the outdated-binary hint). " +
			"Without it every failing command prints its message twice.")
	}
	if !root.SilenceUsage {
		t.Fatal("root must set SilenceUsage: a full usage dump after a runtime " +
			"error buries the message that says what went wrong.")
	}
}
