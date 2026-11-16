package useragent

import "testing"

func TestSet(t *testing.T) {
	if got := String(); got != "burp/dev (+https://github.com/goodtune/burp)" {
		t.Fatalf("default %q", got)
	}
	Set("d70c79f")
	t.Cleanup(func() { Set("dev") })
	if got := String(); got != "burp/d70c79f (+https://github.com/goodtune/burp)" {
		t.Fatalf("after Set %q", got)
	}
}
