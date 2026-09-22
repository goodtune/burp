package diff

import "testing"

const sample = `@@ -1,4 +1,5 @@ package main
 import "fmt"
-func main() {
+func main() { // entry
+	x := 1
 	fmt.Println("hi")
 }
@@ -10,2 +11,2 @@
-old
+new
\ No newline at end of file`

func TestParse(t *testing.T) {
	f, err := Parse(sample)
	if err != nil {
		t.Fatal(err)
	}
	if f.Truncated || len(f.Hunks) != 2 {
		t.Fatalf("hunks = %d", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 4 || h.NewStart != 1 || h.NewLines != 5 || h.Section != "package main" {
		t.Fatalf("header %+v", h)
	}
	want := []Line{
		{Context, 1, 1, `import "fmt"`},
		{Del, 2, 0, "func main() {"},
		{Add, 0, 2, "func main() { // entry"},
		{Add, 0, 3, "\tx := 1"},
		{Context, 3, 4, "\tfmt.Println(\"hi\")"},
		{Context, 4, 5, "}"},
	}
	if len(h.Lines) != len(want) {
		t.Fatalf("lines = %+v", h.Lines)
	}
	for i, l := range want {
		if h.Lines[i] != l {
			t.Fatalf("line %d = %+v want %+v", i, h.Lines[i], l)
		}
	}
	h2 := f.Hunks[1]
	if h2.Lines[0].OldNo != 10 || h2.Lines[1].NewNo != 11 || h2.Lines[2].Kind != NoNewline {
		t.Fatalf("hunk 2 %+v", h2.Lines)
	}
	if a, d := f.Stats(); a != 3 || d != 2 {
		t.Fatalf("stats %d %d", a, d)
	}
	if h.Lines[1].Prefix() != "-" || h.Lines[2].Prefix() != "+" || h.Lines[0].Prefix() != " " || h2.Lines[2].Prefix() != "\\" {
		t.Fatal("prefix")
	}
}

func TestParseEmptyAndSingleLineRanges(t *testing.T) {
	f, err := Parse("")
	if err != nil || !f.Truncated {
		t.Fatal("empty patch should be truncated")
	}
	f, err = Parse("@@ -1 +1 @@\n-a\n+b")
	if err != nil {
		t.Fatal(err)
	}
	if f.Hunks[0].OldLines != 1 || f.Hunks[0].NewLines != 1 {
		t.Fatalf("%+v", f.Hunks[0])
	}
	if _, err := Parse("@@ nope @@"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Parse("@@ -x +1 @@"); err == nil {
		t.Fatal("expected error")
	}
}

func TestSplitRows(t *testing.T) {
	f, _ := Parse(sample)
	rows := SplitRows(f.Hunks[0])
	// context, (del|add), (nil|add), context, context
	if len(rows) != 5 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Left == nil || rows[0].Right == nil || rows[0].Left.Text != rows[0].Right.Text {
		t.Fatal("context row")
	}
	if rows[1].Left == nil || rows[1].Left.Kind != Del || rows[1].Right == nil || rows[1].Right.Kind != Add {
		t.Fatal("paired row")
	}
	if rows[2].Left != nil || rows[2].Right == nil || rows[2].Right.NewNo != 3 {
		t.Fatal("unpaired add row")
	}
	rows2 := SplitRows(f.Hunks[1])
	if len(rows2) != 2 || rows2[1].Left.Kind != NoNewline {
		t.Fatalf("rows2 = %+v", rows2)
	}
}
