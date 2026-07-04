package tui

import "testing"

func TestParseList(t *testing.T) {
	if got := parseList(" a , ,b "); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("parseList = %v, want [a b]", got)
	}
	if got := parseList(""); got != nil {
		t.Errorf("parseList empty = %v, want nil", got)
	}
}

func TestParseReplicas(t *testing.T) {
	if n, err := parseReplicas("", 1); err != nil || n != 1 {
		t.Errorf("empty replicas = %d,%v, want 1,nil", n, err)
	}
	if n, err := parseReplicas("3", 1); err != nil || n != 3 {
		t.Errorf("replicas = %d,%v, want 3,nil", n, err)
	}
	if _, err := parseReplicas("xyz", 1); err == nil {
		t.Error("expected error for non-numeric replicas")
	}
}
