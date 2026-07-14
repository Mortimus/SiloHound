package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	url      string
	username string
	password string
	httpCli  *http.Client
}

func NewClient(url, username, password string) *Client {
	return &Client{
		url:      url, // e.g. http://127.0.0.1:7474
		username: username,
		password: password,
		httpCli:  &http.Client{Timeout: 30 * time.Second},
	}
}

type neoRequest struct {
	Statements []statement `json:"statements"`
}

type statement struct {
	Statement  string                 `json:"statement"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

func (c *Client) RunQuery(cypher string, params map[string]interface{}) error {
	reqBody := neoRequest{
		Statements: []statement{
			{
				Statement:  cypher,
				Parameters: params,
			},
		},
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", c.url+"/db/neo4j/tx/commit", bytes.NewBuffer(b))
	if err != nil {
		return err
	}

	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("neo4j returned status %d", resp.StatusCode)
	}

	// Parse response to check for errors
	var resBody struct {
		Errors []struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&resBody); err != nil {
		return err
	}

	if len(resBody.Errors) > 0 {
		return fmt.Errorf("neo4j error: %s: %s", resBody.Errors[0].Code, resBody.Errors[0].Message)
	}

	return nil
}

func (c *Client) MarkUserOwned(username, password, nthash string, cracked bool) error {
	// Query to find user and update props
	// Note: matching on name (case insensitive ideally, but strict usually fine for AD)
	// We set owned=true, cracked=true, password=..., nthash=...

	// BloodHound convention: owned is boolean. cracked, password, nt_hash are strings/bool.
	// Max uses: SET n.owned=true, n.password="...", n.nt_hash="..."

	query := `
		MATCH (u:User) 
		WHERE toUpper(u.name) = toUpper($name) 
		SET u.owned=true, u.cracked=$cracked, u.password=$password, u.nthash=$nthash, u.owned_reason="Password Cracked"
	`
	// Note: some DBs use u.nt_hash, others u.nthash. Max uses nt_hash. I will use nt_hash to match Max.
	query = `
		MATCH (u:User) 
		WHERE toUpper(u.name) = toUpper($name) 
		SET u.owned=true, u.cracked=$cracked, u.password=$password, u.nt_hash=$nthash
	`

	params := map[string]interface{}{
		"name":     username,
		"cracked":  cracked,
		"password": password,
		"nthash":   nthash,
	}

	return c.RunQuery(query, params)
}

type GroupStat struct {
	GroupName    string
	Count        int
	TotalMembers int
	Percent      float64
	Users        []string // Usernames
}

func (c *Client) GetCrackedHighValue() ([]GroupStat, error) {
	// Query to find cracked users in specific high value groups
	// We want to return a list of groups and the cracked users in them

	// Groups to check: Domain Admins, Enterprise Admins, Schema Admins, Administrators
	// Note: We'll do a generic "High Value" check or specific lists.
	// Max does specific tables for DAs, EAs.

	targetGroups := []string{"DOMAIN ADMINS", "ENTERPRISE ADMINS", "SCHEMA ADMINS", "ADMINISTRATORS", "ACCOUNT OPERATORS", "BACKUP OPERATORS", "PRINT OPERATORS", "SERVER OPERATORS"}

	var stats []GroupStat

	for _, group := range targetGroups {
		// Find users who are owned and members of (nested) group
		// Matching on name contains group name (handled by toUpper)
		query := `
			MATCH (u:User {owned: true})
			MATCH (g:Group) WHERE toUpper(g.name) CONTAINS $group
			MATCH (u)-[:MemberOf*1..]->(g)
			RETURN u.name
		`

		reqBody := neoRequest{
			Statements: []statement{
				{
					Statement: query,
					Parameters: map[string]interface{}{
						"group": group,
					},
				},
			},
		}

		b, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", c.url+"/db/neo4j/tx/commit", bytes.NewBuffer(b))
		req.SetBasicAuth(c.username, c.password)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpCli.Do(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()

		var resBody struct {
			Results []struct {
				Data []struct {
					Row []interface{} `json:"row"`
				} `json:"data"`
			} `json:"results"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&resBody); err != nil {
			continue
		}

		if len(resBody.Results) > 0 {
			var users []string
			for _, d := range resBody.Results[0].Data {
				if len(d.Row) > 0 {
					if name, ok := d.Row[0].(string); ok {
						users = append(users, name)
					}
				}
			}
			if len(users) > 0 {
				stats = append(stats, GroupStat{
					GroupName: group,
					Count:     len(users),
					Users:     users,
				})
			}
		}
	}
	return stats, nil
}

// GetUsers executes a cypher query and returns the first column as a list of strings
func (c *Client) GetUsers(query string, params map[string]interface{}) ([]string, error) {
	if params == nil {
		params = make(map[string]interface{})
	}

	reqBody := neoRequest{
		Statements: []statement{
			{
				Statement:  query,
				Parameters: params,
			},
		},
	}

	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", c.url+"/db/neo4j/tx/commit", bytes.NewBuffer(b))
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var resBody struct {
		Results []struct {
			Data []struct {
				Row []interface{} `json:"row"`
			} `json:"data"`
		} `json:"results"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&resBody); err != nil {
		return nil, err
	}

	if len(resBody.Errors) > 0 {
		return nil, fmt.Errorf("neo4j error: %s", resBody.Errors[0].Message)
	}

	var users []string
	if len(resBody.Results) > 0 {
		for _, d := range resBody.Results[0].Data {
			if len(d.Row) > 0 {
				if name, ok := d.Row[0].(string); ok {
					users = append(users, name)
				}
			}
		}
	}
	return users, nil
}

// GetGroupStats calculates the cracked percentage for all groups
func (c *Client) GetGroupStats() ([]GroupStat, error) {
	// Simple query: For each group, count total members (recursive) and owned members.
	// This can be heavy on large DBs, but standard for Max report.
	// Limiting to groups with at least 1 member to avoid noise.

	query := `
		MATCH (g:Group)
		OPTIONAL MATCH (u:User)-[:MemberOf*1..]->(g)
		WITH g, count(u) as total, count(u) filter where u.owned=true as cracked
		WHERE total > 0 AND cracked > 0
		RETURN g.name, total, cracked, (toFloat(cracked)/total)*100 as pct
		ORDER BY pct DESC
	`

	type row struct {
		Name    string `json:"row"` // Actually need careful parsing order
		Total   int
		Cracked int
		Pct     float64
	}

	// Using existing generic request, but need custom parsing for multiple columns
	reqBody := neoRequest{Statements: []statement{{Statement: query}}}
	b, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", c.url+"/db/neo4j/tx/commit", bytes.NewBuffer(b))
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpCli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var resBody struct {
		Results []struct {
			Data []struct {
				Row []interface{} `json:"row"`
			} `json:"data"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&resBody); err != nil {
		return nil, err
	}

	var stats []GroupStat
	if len(resBody.Results) > 0 {
		for _, d := range resBody.Results[0].Data {
			if len(d.Row) == 4 {
				name, _ := d.Row[0].(string)
				total, _ := d.Row[1].(float64) // JSON numbers are floats
				cracked, _ := d.Row[2].(float64)
				pct, _ := d.Row[3].(float64)

				stats = append(stats, GroupStat{
					GroupName:    name,
					TotalMembers: int(total),
					Count:        int(cracked),
					Percent:      pct,
				})
			}
		}
	}
	return stats, nil
}
