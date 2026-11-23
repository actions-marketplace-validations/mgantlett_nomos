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

	if HasUnquotedShellChainingOrSub(`git status '; echo pwn' '$(ignored)' '{"a":1,"b":2}' "${HOME}/ok"`) {
		t.Fatal("expected quoted separators, JSON literals, and parameter expansion to be ignored")
	}
	for _, risky := range []string{
		`git status && cat .env`,
		`git status | tee x`,
		"echo \"$(cat .env)\"",
		"echo \"`id`\"",
		`cat <(/root/.ssh/id_rsa)`,
		`tee >(/tmp/out)`,
		`cat $'\x2froot\x2f.ssh\x2fid_rsa'`,
		`{cat,/root/.ssh/id_rsa}`,
	} {
		if !HasUnquotedShellChainingOrSub(risky) {
			t.Fatalf("expected chaining/subshell/dialect expansion detected for %q", risky)
		}
	}
}
