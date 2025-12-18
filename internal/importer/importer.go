package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
)

const QUERY_LIBRARY_URL = "https://github.com/SpecterOps/BloodHoundQueryLibrary"

type BloodHoundLegacyQuery struct {
	Name     string                `json:"name"`
	Category string                `json:"category"`
	Queries  []BloodHoundQueryItem `json:"queryList"`
}

type BloodHoundQueryItem struct {
	Final             bool              `json:"final"`
	Title             string            `json:"title,omitempty"`
	Query             string            `json:"query"`
	AllowCollapse     bool              `json:"allowCollapse,omitempty"`
	Props             map[string]string `json:"props,omitempty"`
	RequireNodeSelect bool              `json:"requireNodeSelect,omitempty"`
	StartNode         string            `json:"startNode,omitempty"`
	EndNode           string            `json:"endNode,omitempty"`
}

type BloodHoundLegacyQueries struct {
	Queries []BloodHoundLegacyQuery `json:"queries"`
}

type BloodHoundQuery struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Query       string `json:"query"`
}

type BloodHoundQueries struct {
	Queries []BloodHoundQuery `json:"queries"`
}

func ReadLegacyQueries(path string) (BloodHoundLegacyQueries, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return BloodHoundLegacyQueries{}, err
	}
	var LegacyQueries BloodHoundLegacyQueries
	err = json.Unmarshal(data, &LegacyQueries)
	if err != nil {
		return BloodHoundLegacyQueries{}, err
	}
	return LegacyQueries, nil
}

func CloneQueryLibrary(dest string) error {
	// Remove if exists to ensure clean clone
	os.RemoveAll(dest)

	_, err := git.PlainClone(dest, false, &git.CloneOptions{
		URL:      QUERY_LIBRARY_URL,
		Progress: os.Stdout,
	})
	return err
}

func LegacyToNewQueries(legacy BloodHoundLegacyQueries) BloodHoundQueries {
	var newQueries BloodHoundQueries
	for _, q := range legacy.Queries {
		query := LegacyQueryToSingleModernQuery(q)
		if query.Description != "Unimplemented" {
			newQueries.Queries = append(newQueries.Queries, query)
		}
	}
	return newQueries
}

func LegacyQueryToSingleModernQuery(legacy BloodHoundLegacyQuery) BloodHoundQuery {
	if len(legacy.Queries) == 1 {
		name := fmt.Sprintf("[%s] %s", legacy.Category, legacy.Name)
		query := ""
		if legacy.Queries[0].Props != nil {
			query = inlinePropValue(legacy.Queries[0].Query, legacy.Queries[0].Props)
		} else {
			query = legacy.Queries[0].Query
		}
		return BloodHoundQuery{
			Name:        name,
			Description: legacy.Name,
			Query:       query,
		}
	}
	name := fmt.Sprintf("Multi Query not implemented yet: %s\n", legacy.Name)
	return BloodHoundQuery{
		Name:        name,
		Description: "Unimplemented",
		Query:       "//Unimplemented",
	}
}

func inlinePropValue(query string, props map[string]string) string {
	for k, v := range props {
		variable := fmt.Sprintf("$%s", k)
		quoted := fmt.Sprintf("'%s'", v)
		query = strings.ReplaceAll(query, variable, quoted)
	}
	return query
}

// Function helper to scan a directory for JSON files and try to parse them
func LoadQueriesFromDir(dir string) (BloodHoundQueries, error) {
	var allQueries BloodHoundQueries
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".json") {
			// Try parsing as legacy
			lq, err := ReadLegacyQueries(path)
			if err == nil && len(lq.Queries) > 0 {
				converted := LegacyToNewQueries(lq)
				allQueries.Queries = append(allQueries.Queries, converted.Queries...)
			}
			// Future: Try parsing as modern format if SpecterOps library uses different format
		}
		return nil
	})
	return allQueries, err
}
