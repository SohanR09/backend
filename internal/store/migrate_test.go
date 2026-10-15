package store

import "testing"

func TestSplitStatements(t *testing.T) {
	got := splitStatements("-- header\nCREATE TABLE a (x int);\n\n-- note\nCREATE INDEX i ON a (x);\n")
	if len(got) != 2 || got[0] != "CREATE TABLE a (x int)" || got[1] != "CREATE INDEX i ON a (x)" {
		t.Fatalf("got %q", got)
	}
	// A semicolon inside a comment must not split the statement.
	got = splitStatements("CREATE TABLE a (\n  -- note; with semicolon\n  x int\n);")
	if len(got) != 1 {
		t.Fatalf("comment semicolon split the statement: %q", got)
	}
	if n := len(splitStatements(schemaSQL)); n < 10 {
		t.Fatalf("embedded schema produced only %d statements", n)
	}
}
