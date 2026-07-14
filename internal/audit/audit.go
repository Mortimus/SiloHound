package audit

import (
	"bufio"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type User struct {
	Username  string
	Domain    string
	RID       string
	LMHash    string
	NTHash    string
	Plaintext string
	Cracked   bool
}

type StatPair struct {
	Key   string
	Value int
}

type ComplexityStat struct {
	Username string
	Password string
	NTHash   string // NT hash of the password (obfuscated in reports)
	Meets    bool
}

type GroupStat struct {
	GroupName    string
	Count        int      // Cracked members count
	TotalMembers int      // Total members count
	Percent      float64  // Cracked %
	Users        []string // Cracked Usernames
}

type Analysis struct {
	TotalUsers              int
	CrackedUsers            int
	CrackedPercentage       float64
	UniqueHashes            int
	UniqueCracked           int
	UniqueCrackedPercentage float64
	TopPasswords            []StatPair
	PasswordLengths         []StatPair // Length -> Count
	PasswordReuse           []StatPair // Password -> Count
	TopBaseWords            []StatPair // BaseWord -> Count
	ComplexityStats         []ComplexityStat
	ExposedCreds            []*User     // Users with cracked passwords
	GroupStats              []GroupStat // From Neo4j

	// Max Parity
	LMHashCount             int
	LMHashUnique            int
	UsernameMatchesPassword []*User

	// Dynamic Report Table
	ReportEntries []ReportEntry

	// Special Stats
	GroupsByPercentage []GroupStat
}

type ReportEntry struct {
	Description string
	Count       interface{} // Can be int or string (for percentages)
	Users       []string
	IsPercent   bool
	HasDetails  bool
}

func ParseNTDS(path string) ([]*User, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var users []*User
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			continue
		}
		// Format: User:RID:LM:NT:::
		// User can be Domain\User or just User
		fullUser := parts[0]
		var username, domain string
		if strings.Contains(fullUser, "\\") {
			up := strings.SplitN(fullUser, "\\", 2)
			domain = up[0]
			username = up[1]
		} else {
			username = fullUser
		}

		u := &User{
			Username: username,
			Domain:   domain,
			RID:      parts[1],
			LMHash:   parts[2],
			NTHash:   parts[3],
		}

		// Skip machine accounts (roughly)
		if strings.HasSuffix(u.Username, "$") {
			continue
		}

		users = append(users, u)
	}
	return users, scanner.Err()
}

func ParsePotfile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	pot := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: hash:plaintext
		// Max repo handles $NT$ prefix removal
		cleanLine := strings.ReplaceAll(line, "$NT$", "")
		cleanLine = strings.ReplaceAll(cleanLine, "$LM$", "")

		parts := strings.SplitN(cleanLine, ":", 2)
		if len(parts) == 2 {
			hash := parts[0]
			plain := parts[1]
			pot[hash] = plain
		}
	}
	return pot, scanner.Err()
}

func Analyze(users []*User, pot map[string]string) *Analysis {
	a := &Analysis{}
	a.TotalUsers = len(users)

	uniqueHashes := make(map[string]bool)
	uniqueCrackedHashes := make(map[string]bool)
	passCounts := make(map[string]int)
	baseWordCounts := make(map[string]int)
	lengthCounts := make(map[int]int)

	// Max Parity collections
	uniqueLM := make(map[string]bool)

	for _, u := range users {
		uniqueHashes[u.NTHash] = true

		// Check LM
		if u.LMHash != "" && u.LMHash != "aad3b435b51404eeaad3b435b51404ee" {
			a.LMHashCount++
			uniqueLM[u.LMHash] = true
		}

		// Correlate (NT)
		if plain, ok := pot[u.NTHash]; ok {
			u.Cracked = true
			u.Plaintext = plain

			// Check Username == Password
			if strings.EqualFold(u.Username, plain) {
				a.UsernameMatchesPassword = append(a.UsernameMatchesPassword, u)
			}

			// Only count once for stats
			if !containsUser(a.ExposedCreds, u) {
				a.ExposedCreds = append(a.ExposedCreds, u)
				a.CrackedUsers++
				uniqueCrackedHashes[u.NTHash] = true
				passCounts[plain]++
				lengthCounts[len(plain)]++

				a.ComplexityStats = append(a.ComplexityStats, ComplexityStat{
					Username: u.Username,
					Password: plain,
					NTHash:   u.NTHash,
					Meets:    checkComplexity(plain),
				})

				// Base Word Analysis
				base := extractBaseWord(plain)
				if base != "" {
					baseWordCounts[base]++
				}
			}
		} else if u.LMHash != "" && u.LMHash != "aad3b435b51404eeaad3b435b51404ee" {
			// Try LM
			if plain, ok := pot[u.LMHash]; ok {
				u.Cracked = true
				u.Plaintext = plain
				// Note: typically LM implies ignore case, but for simple stats we just take it

				if strings.EqualFold(u.Username, plain) {
					a.UsernameMatchesPassword = append(a.UsernameMatchesPassword, u)
				}

				if !containsUser(a.ExposedCreds, u) {
					a.ExposedCreds = append(a.ExposedCreds, u)
					a.CrackedUsers++
					// Don't count unique NT cracked hash if we only cracked LM, theoretically
					passCounts[plain]++
					lengthCounts[len(plain)]++

					a.ComplexityStats = append(a.ComplexityStats, ComplexityStat{
						Username: u.Username,
						Password: plain,
						NTHash:   u.NTHash,
						Meets:    checkComplexity(plain),
					})

					// Base Word Analysis
					base := extractBaseWord(plain)
					if base != "" {
						baseWordCounts[base]++
					}
				}
			}
		}
	}

	a.UniqueHashes = len(uniqueHashes)
	a.UniqueCracked = len(uniqueCrackedHashes)
	a.LMHashUnique = len(uniqueLM)

	if a.TotalUsers > 0 {
		a.CrackedPercentage = (float64(a.CrackedUsers) / float64(a.TotalUsers)) * 100
	}
	if a.UniqueHashes > 0 {
		a.UniqueCrackedPercentage = (float64(a.UniqueCracked) / float64(a.UniqueHashes)) * 100
	}

	// Top Passwords / Reuse
	for p, c := range passCounts {
		if c > 1 {
			a.PasswordReuse = append(a.PasswordReuse, StatPair{Key: p, Value: c})
		}
	}
	sortStats(a.PasswordReuse)

	// Password Lengths
	for l, c := range lengthCounts {
		a.PasswordLengths = append(a.PasswordLengths, StatPair{
			Key:   strconv.Itoa(l),
			Value: c,
		})
	}
	// Sort lengths by count (descending) or by length (ascending)?
	// Max sorts by count desc.
	sortStats(a.PasswordLengths)

	// Top Base Words
	for w, c := range baseWordCounts {
		if c > 1 {
			a.TopBaseWords = append(a.TopBaseWords, StatPair{Key: w, Value: c})
		}
	}
	sortStats(a.TopBaseWords)

	// Sort complexity stats by username
	sort.Slice(a.ComplexityStats, func(i, j int) bool {
		return a.ComplexityStats[i].Username < a.ComplexityStats[j].Username
	})

	// Sort username matches password by username
	sort.Slice(a.UsernameMatchesPassword, func(i, j int) bool {
		return a.UsernameMatchesPassword[i].Username < a.UsernameMatchesPassword[j].Username
	})

	return a
}

func containsUser(list []*User, u *User) bool {
	for _, e := range list {
		if e == u {
			return true
		}
	}
	return false
}

func checkComplexity(s string) bool {
	var hasUpper, hasLower, hasDigit, hasSpecial bool
	special := "`~!@#$%^&*()-_=+,<.>/?;:\"'{}[]|\\"
	for _, c := range s {
		if unicode.IsUpper(c) {
			hasUpper = true
		}
		if unicode.IsLower(c) {
			hasLower = true
		}
		if unicode.IsDigit(c) {
			hasDigit = true
		}
		if strings.ContainsRune(special, c) {
			hasSpecial = true
		}
	}
	count := 0
	if hasUpper {
		count++
	}
	if hasLower {
		count++
	}
	if hasDigit {
		count++
	}
	if hasSpecial {
		count++
	}
	return count >= 3
}

func sortStats(s []StatPair) {
	sort.Slice(s, func(i, j int) bool {
		return s[i].Value > s[j].Value
	})
}

func extractBaseWord(s string) string {
	// Simple heuristic: remove trailing digits and special chars
	// Keep text
	// This is a naive implementation matching basic DPAT logic
	runes := []rune(s)
	end := len(runes)
	for end > 0 {
		r := runes[end-1]
		if unicode.IsDigit(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			end--
		} else {
			break
		}
	}
	// Start trim? Usually we care about the "Root" word.
	// Max DPAT often just regex replaces [\d\W] with empty? No, that destroys structure.
	// Often it's strip digits/specials from end.

	base := string(runes[:end])

	// Also strip leading digits/specials?
	// e.g. !Pass123 -> Pass
	start := 0
	runes = []rune(base) // re-rune
	for start < len(runes) {
		r := runes[start]
		if unicode.IsDigit(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			start++
		} else {
			break
		}
	}
	if start >= len(runes) {
		return ""
	}
	return strings.ToLower(string(runes[start:]))
}
