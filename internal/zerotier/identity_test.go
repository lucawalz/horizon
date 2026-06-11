package zerotier

import "testing"

func TestParseIdentity(t *testing.T) {
	const sample = "8d9e97c05c:0:7a28a9f26dad14c6:e15bdf6ebd867dd6"
	id, err := ParseIdentity(sample + "\n")
	if err != nil {
		t.Fatalf("ParseIdentity: %v", err)
	}
	if id.MemberID != "8d9e97c05c" {
		t.Errorf("MemberID = %q, want 8d9e97c05c", id.MemberID)
	}
	if id.Secret != sample {
		t.Errorf("Secret = %q, want %q", id.Secret, sample)
	}
	if id.Public != "8d9e97c05c:0:7a28a9f26dad14c6" {
		t.Errorf("Public = %q", id.Public)
	}
}

func TestParseIdentityMalformed(t *testing.T) {
	for _, in := range []string{"", "abc", "a:0:b", ":0:b:c"} {
		if _, err := ParseIdentity(in); err == nil {
			t.Errorf("ParseIdentity(%q) = nil err, want error", in)
		}
	}
}
