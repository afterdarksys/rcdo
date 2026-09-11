package main

import "testing"

func TestSummarize(t *testing.T) {
	r := summarize("Café café! shell, SHELL. 世界 42", 3)
	if r.Words != 6 || r.Unique != 4 || len(r.Top) != 3 {
		t.Fatalf("unexpected report: %+v", r)
	}
	if r.Top[0] != (Word{"café", 2}) || r.Top[1] != (Word{"shell", 2}) {
		t.Fatalf("wrong counts or tie ordering: %+v", r.Top)
	}
}

func TestEmpty(t *testing.T) {
	r := summarize(" \n---", 10)
	if r.Words != 0 || len(r.Top) != 0 {
		t.Fatalf("unexpected report: %+v", r)
	}
}
