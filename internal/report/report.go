package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"os"
	"time"

	"github.com/Mortimus/SiloHound/internal/audit"
)

//go:embed template.html
var defaultTemplate string

type ReportData struct {
	Date     string
	Stats    *audit.Analysis
	TopReuse []audit.StatPair
}

func Generate(path string, stats *audit.Analysis, customTemplatePath string) error {
	tmplContent := defaultTemplate

	if customTemplatePath != "" {
		b, err := os.ReadFile(customTemplatePath)
		if err != nil {
			return fmt.Errorf("failed to read custom template: %w", err)
		}
		tmplContent = string(b)
	}

	funcMap := template.FuncMap{
		"obfuscate": func(s string) string {
			if len(s) <= 2 {
				return "***"
			}
			return s[:1] + "***" + s[len(s)-1:]
		},
		"obfuscateHash": func(s string) string {
			if len(s) <= 8 {
				return "********"
			}
			return s[:4] + "********" + s[len(s)-4:]
		},
	}

	t, err := template.New("report").Funcs(funcMap).Parse(tmplContent)
	if err != nil {
		return err
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Limit TopReuse to 10
	topReuse := stats.PasswordReuse
	if len(topReuse) > 10 {
		topReuse = topReuse[:10]
	}

	data := ReportData{
		Date:     time.Now().Format("2006-01-02 15:04:05"),
		Stats:    stats,
		TopReuse: topReuse,
	}

	return t.Execute(f, data)
}
