package capbac

import (
	"reflect"
	"testing"
)

func TestShellLex_SplitTokenizeAndChaining(t *testing.T) {
	segs := SplitUnquotedShellSegments(`PYTHON="3" && cat 'a;b|c' > out.py || echo "done"`)
	wantSegs := []string{`PYTHON="3"`, `cat 'a;b|c' > out.py`, `echo "done"`}
	if !reflect.DeepEqual(segs, wantSegs) {
		t.Fatalf("SplitUnquotedShellSegments = %#v, want %#v", segs, wantSegs)
	}

	words := TokenizeQuotedShellWords(`env FOO="bar baz" 'quoted;val' \"esc`)
	wantWords := []string{"env", "FOO=bar baz", "quoted;val", `"esc`}
	if !reflect.DeepEqual(words, wantWords) {
		t.Fatalf("TokenizeQuotedShellWords = %#v, want %#v", words, wantWords)
	}

	if HasUnquotedShellChainingOrSub(`git status '; echo pwn' '$(ignored)'`) {
		t.Fatal("expected single-quoted separators and subshells to be ignored")
	}
	for _, risky := range []string{
		`git status && cat .env`,
		`git status | tee x`,
		"echo \"$(cat .env)\"",
		"echo \"`id`\"",
	} {
		if !HasUnquotedShellChainingOrSub(risky) {
			t.Fatalf("expected chaining/subshell detected for %q", risky)
		}
	}
}
