package tools

import "testing"

func TestComputeDiffDeleteLinesDoesNotMarkShiftedTail(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff(
		"example.txt",
		"l1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\nl9\n",
		"l1\nl2\nl3\nl5\nl6\nl7\nl8\nl9\n",
		"delete_lines",
	)
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,7 +1,6 @@\n" +
		" l1\n" +
		" l2\n" +
		" l3\n" +
		"-l4\n" +
		" l5\n" +
		" l6\n" +
		" l7\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestComputeDiffDeleteLinesIsMinimal(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff("example.txt", "one\ntwo\nthree\nfour\nfive\n", "one\ntwo\nfive\n", "delete_lines")
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,5 +1,3 @@\n" +
		" one\n" +
		" two\n" +
		"-three\n" +
		"-four\n" +
		" five\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestComputeDiffInsertIsMinimal(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff("example.txt", "one\ntwo\nthree\n", "one\ntwo\ninserted\nthree\n", "insert")
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,3 +1,4 @@\n" +
		" one\n" +
		" two\n" +
		"+inserted\n" +
		" three\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}

func TestComputeDiffReplaceLinesIsMinimal(t *testing.T) {
	e := &Executor{}
	diff := e.computeDiff("example.txt", "one\nold\nthree\n", "one\nnew\nthree\n", "replace_lines")
	want := "--- a/example.txt\n" +
		"+++ b/example.txt\n" +
		"@@ -1,3 +1,3 @@\n" +
		" one\n" +
		"-old\n" +
		"+new\n" +
		" three\n"
	if diff != want {
		t.Fatalf("unexpected diff\nwant:\n%s\ngot:\n%s", want, diff)
	}
}
