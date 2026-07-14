package audit

import (
	"os"
	"testing"
)

func TestAnalyze(t *testing.T) {
	users := []*User{
		{Username: "user1", NTHash: "hash1"},
		{Username: "user2", NTHash: "hash2"},
		{Username: "user3", NTHash: "hash1"}, // Reuse
	}
	pot := map[string]string{
		"hash1": "password123",
	}

	stats := Analyze(users, pot)

	if stats.TotalUsers != 3 {
		t.Errorf("Expected 3 total users, got %d", stats.TotalUsers)
	}
	if stats.CrackedUsers != 2 {
		t.Errorf("Expected 2 cracked users, got %d", stats.CrackedUsers)
	}
	if stats.CrackedPercentage != 50.0 {
		t.Errorf("Expected 50.0%% cracked, got %f", stats.CrackedPercentage)
	}

	// New Max Parity Checks
	if stats.LMHashCount != 1 {
		t.Errorf("Expected 1 LM hash, got %d", stats.LMHashCount)
	}
	if stats.LMHashUnique != 1 {
		t.Errorf("Expected 1 unique LM hash, got %d", stats.LMHashUnique)
	}
	// user1 has NTHash1 (cracked). user2 has NTHash2 (not cracked).
	// Unique hashes: 2. Unique cracked: 1.
	// Expected UniqueCrackedPercentage: 50.0
	if stats.UniqueCrackedPercentage != 50.0 {
		t.Errorf("Expected 50.0%% unique cracked, got %f", stats.UniqueCrackedPercentage)
	}
	if len(stats.PasswordReuse) != 1 {
		t.Errorf("Expected 1 reused password entry, got %d", len(stats.PasswordReuse))
	}
	if stats.PasswordReuse[0].Key != "password123" {
		t.Errorf("Expected reused password to be 'password123', got %s", stats.PasswordReuse[0].Key)
	}
	if stats.PasswordReuse[0].Value != 2 {
		t.Errorf("Expected reuse count 2, got %d", stats.PasswordReuse[0].Value)
	}
}

func TestCheckComplexity(t *testing.T) {
	tests := []struct {
		pass string
		want bool
	}{
		{"password", false},
		{"Password123", true}, // Upper, Lower, Digit
		{"pass1!", true},      // Lower, Digit, Special (no upper) -> 3 types -> Valid
		{"123456", false},
		{"Aa1!", true},
	}

	for _, tt := range tests {
		got := checkComplexity(tt.pass)
		if got != tt.want {
			t.Errorf("checkComplexity(%q) = %v, want %v", tt.pass, got, tt.want)
		}
	}
}

func TestParseNTDS(t *testing.T) {
	content := `User1:1000:lm:nt:::
User2:1001:lm:nt2:::
Machine$:1002:lm:nt3:::
`
	tmpfile, err := os.CreateTemp("", "ntds")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	users, err := ParseNTDS(tmpfile.Name())
	if err != nil {
		t.Fatalf("ParseNTDS failed: %v", err)
	}

	if len(users) != 2 {
		t.Errorf("Expected 2 users (skipped machine), got %d", len(users))
	}
	if users[0].Username != "User1" {
		t.Errorf("Expected User1, got %s", users[0].Username)
	}
}

func TestParsePotfile(t *testing.T) {
	content := `hash1:plain1
$NT$hash2:plain2
`
	tmpfile, err := os.CreateTemp("", "pot")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	pot, err := ParsePotfile(tmpfile.Name())
	if err != nil {
		t.Fatalf("ParsePotfile failed: %v", err)
	}

	if len(pot) != 2 {
		t.Errorf("Expected 2 entries, got %d", len(pot))
	}
	if pot["hash1"] != "plain1" {
		t.Errorf("Expected plain1 for hash1, got %s", pot["hash1"])
	}
	if pot["hash2"] != "plain2" {
		t.Errorf("Expected plain2 for hash2, got %s", pot["hash2"])
	}
}
