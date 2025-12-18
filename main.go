package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mortimus/SiloHound/internal/audit"
	"github.com/Mortimus/SiloHound/internal/database"
	"github.com/Mortimus/SiloHound/internal/docker"
	"github.com/Mortimus/SiloHound/internal/graph"
	"github.com/Mortimus/SiloHound/internal/importer"
	"github.com/Mortimus/SiloHound/internal/report"
)

func main() {
	name := flag.String("name", "", "Project Name")
	path := flag.String("path", "", "Path to store data folders (default: current directory)")
	list := flag.Bool("list", false, "List known projects")
	clean := flag.Bool("clean", false, "Clean/Delete project (requires -name)")
	stop := flag.Bool("stop", false, "Stop all containers for project (requires -name)")
	move := flag.String("move", "", "Move project to new path (requires -name)")
	custom := flag.String("custom", "", "Path to custom queries.json (optional)")
	cloneQueries := flag.Bool("clone-queries", false, "Clone SpecterOps Query Library")
	pull := flag.Bool("pull", true, "Pull images before starting")

	// Password Audit Flags
	auditNTDS := flag.String("audit-ntds", "", "Path to secretsdump output (User:RID:LM:NT:...)")
	auditCracked := flag.String("audit-cracked", "", "Path to cracked hashes (hash:plain)")
	auditTemplate := flag.String("audit-template", "", "Path to custom HTML report template")

	flag.Parse()

	// Initialize Database
	db, err := database.InitDB()
	if err != nil {
		log.Fatalf("Failed to init database: %v", err)
	}
	defer db.Close()

	// List Projects
	if *list {
		projects, err := db.ListProjects()
		if err != nil {
			log.Fatalf("Failed to list projects: %v", err)
		}
		fmt.Printf("Known Projects:\n")
		for _, p := range projects {
			fmt.Printf("- %s (Created: %s, Path: %s)\n", p.Name, p.CreatedAt.Format(time.RFC822), p.Path)
		}
		return
	}

	if *name == "" {
		log.Fatal("-name is required")
	}

	// Docker Manager
	ctx := context.Background()
	mgr, err := docker.NewManager(ctx)
	if err != nil {
		log.Fatalf("Failed to create docker client: %v", err)
	}
	defer mgr.Close()

	// Clean Project
	if *clean {
		// Stop containers first
		fmt.Printf("Stopping containers for project %s...\n", *name)
		if err := mgr.StopProjectContainers(*name); err != nil {
			fmt.Printf("Warning: failed to stop containers: %v\n", err)
		}

		err := db.DeleteProject(*name)
		if err != nil {
			log.Fatalf("Failed to delete project: %v", err)
		}
		fmt.Printf("Project %s removed from database.\n", *name)
		return
	}

	// Stop Project
	if *stop {
		fmt.Printf("Stopping containers for project %s...\n", *name)
		if err := mgr.StopProjectContainers(*name); err != nil {
			log.Fatalf("Failed to stop containers: %v", err)
		}
		fmt.Printf("Project %s containers stopped.\n", *name)
		return
	}

	// Move Project
	if *move != "" {
		existing, err := db.GetProject(*name)
		if err != nil {
			log.Fatal(err)
		}
		if existing == nil {
			log.Fatalf("Project %s not found", *name)
		}

		newPath, err := filepath.Abs(*move)
		if err != nil {
			log.Fatalf("Invalid path: %v", err)
		}

		if newPath == existing.Path {
			fmt.Println("New path is the same as the current path.")
			return
		}

		// Update DB
		err = db.UpdateProjectPath(*name, newPath)
		if err != nil {
			log.Fatalf("Failed to update project path in DB: %v", err)
		}

		fmt.Printf("Project %s path updated in database.\nOld: %s\nNew: %s\n", *name, existing.Path, newPath)
		fmt.Println("NOTE: This command only updates the database record. You must move the data files manually if needed.")
		return
	}

	// Start Project (Resume or New)

	// Check if already running
	running, err := mgr.IsRunning(*name)
	if err != nil {
		log.Printf("Warning: Failed to check if running: %v", err)
	}
	if running {
		fmt.Printf("WARNING: Project %s appears to be already running. Starting another instance may fail or cause conflicts.\n", *name)
		fmt.Print("Press ENTER to continue anyway, or Ctrl+C to abort...")
		bufio.NewScanner(os.Stdin).Scan()
	}

	// Resolve path
	var workingDir string
	existing, err := db.GetProject(*name)
	if err != nil {
		log.Fatal(err)
	}

	if existing != nil {
		fmt.Printf("Resuming known project %s...\n", existing.Name)
		if *path != "" {
			absPath, _ := filepath.Abs(*path)
			if absPath != existing.Path {
				// Prevent Overwrite - Strict Error
				log.Fatalf("Error: Project '%s' already exists at '%s'.\nYou provided '%s'.\nUse -move at the command line/terminal to change the project location or omit -path to use the existing one.", existing.Name, existing.Path, absPath)
			} else {
				workingDir = existing.Path
			}
		} else {
			workingDir = existing.Path
		}
		fmt.Printf("Project Path: %s\n", workingDir)
	} else {
		// New Project
		if *path == "" {
			wd, err := os.Getwd()
			if err != nil {
				log.Fatal(err)
			}
			workingDir = wd
		} else {
			workingDir = *path
		}

		// Ensure absolute path
		absPath, err := filepath.Abs(workingDir)
		if err != nil {
			log.Fatalf("Failed to resolve absolute path: %v", err)
		}
		workingDir = absPath

		fmt.Printf("New project %s detected. Registering at %s\n", *name, workingDir)
		err = db.AddProject(*name, workingDir)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Create Folders
	createFolders(workingDir)

	// Ensure Network
	netName, err := mgr.EnsureNetwork(*name)
	if err != nil {
		log.Fatalf("Failed to create network: %v", err)
	}

	if *pull {
		fmt.Println("Pulling images...")
		if err := mgr.PullImage(docker.POSTGRESQL); err != nil {
			fmt.Printf("Warning: Failed to pull Postgres: %v\n", err)
		}
		if err := mgr.PullImage(docker.NEO4J); err != nil {
			fmt.Printf("Warning: Failed to pull Neo4j: %v\n", err)
		}
		if err := mgr.PullImage(docker.BLOODHOUND); err != nil {
			fmt.Printf("Warning: Failed to pull BloodHound: %v\n", err)
		}
	}

	// Start Containers
	psqlID, err := mgr.SpawnPostgres(*name, workingDir, netName)
	if err != nil {
		// mgr.StopProjectContainers(*name) // Optional cleanup on fail
		log.Fatalf("Failed to start Postgres: %v", err)
	}
	fmt.Printf("Postgres started (ID: %s)\n", psqlID[:12])

	neo4jID, err := mgr.SpawnNeo4j(*name, workingDir, netName)
	if err != nil {
		log.Fatalf("Failed to start Neo4j: %v", err)
	}
	fmt.Printf("Neo4j started (ID: %s)\n", neo4jID[:12])

	bhID, err := mgr.SpawnBloodhound(*name, netName, "admin", "admin")
	if err != nil {
		log.Fatalf("Failed to start BloodHound: %v", err)
	}
	fmt.Printf("BloodHound started (ID: %s)\n", bhID[:12])

	// Update Password Expiration (1 Year)
	expirationFunc := func() error {
		expDate := time.Now().AddDate(1, 0, 0).Format("2006-01-02 15:04:05")
		sql := fmt.Sprintf("UPDATE auth_secrets SET expires_at='%s' WHERE id='1';", expDate)
		// Execute on Postgres
		return mgr.Exec(psqlID, []string{"psql", "-q", "-U", "bloodhound", "-d", "bloodhound", "-c", sql})
	}

	fmt.Println("Updating password expiration to 1 year...")
	if err := expirationFunc(); err != nil {
		fmt.Printf("Warning: Failed to update password expiration: %v\n", err)
	}

	// Inject Queries into Postgres
	if *custom != "" {
		fmt.Printf("Injecting custom queries from %s...\n", *custom)
		queries, err := importer.ReadLegacyQueries(*custom)
		if err == nil {
			newQueries := importer.LegacyToNewQueries(queries)
			injectQueries(mgr, psqlID, newQueries)
		} else {
			fmt.Printf("Failed to read custom queries: %v\n", err)
		}
	}

	if *cloneQueries {
		fmt.Printf("Cloning Query Library...\n")
		dest := filepath.Join(workingDir, "BloodHoundQueryLibrary")
		err := importer.CloneQueryLibrary(dest)
		if err == nil {
			fmt.Printf("Loading queries from library...\n")
			queries, err := importer.LoadQueriesFromDir(dest)
			if err == nil {
				injectQueries(mgr, psqlID, queries)
			}
		} else {
			fmt.Printf("Failed to clone/load library: %v\n", err)
		}
	}

	// Audit Feature
	if *auditNTDS != "" && *auditCracked != "" {
		fmt.Println("Starting Password Audit...")

		// 1. Parse
		fmt.Println("Parsing NTDS...")
		users, err := audit.ParseNTDS(*auditNTDS)
		if err != nil {
			fmt.Printf("Failed to parse NTDS: %v\n", err)
		} else {
			fmt.Printf("Loaded %d users from NTDS.\n", len(users))

			fmt.Println("Parsing Cracked Passwords...")
			pot, err := audit.ParsePotfile(*auditCracked)
			if err != nil {
				fmt.Printf("Failed to parse cracked file: %v\n", err)
			} else {
				fmt.Printf("Loaded %d cracked hashes.\n", len(pot))

				// 2. Analyze
				fmt.Println("Analyzing and Correlating...")
				stats := audit.Analyze(users, pot)
				fmt.Printf("Cracked %d/%d users (%.2f%%)\n", stats.CrackedUsers, stats.TotalUsers, stats.CrackedPercentage)

				// 3. Update Neo4j
				// We need to wait for Neo4j to be fully ready and accepting HTTP
				// The container is "ready" via logs, but ports might need a second. Use retries.
				fmt.Println("Updating Neo4j with audit data...")
				graphCli := graph.NewClient("http://127.0.0.1:7474", "neo4j", "bloodhoundcommunityedition") // Default creds

				// Optional: Wait for connection
				// For now just try update
				count := 0
				for _, u := range stats.ExposedCreds {
					if err := graphCli.MarkUserOwned(u.Username, u.Plaintext, u.NTHash, true); err != nil {
						// Don't fail hard, just warn
						// fmt.Printf("Failed to update user %s: %v\n", u.Username, err)
					} else {
						count++
					}
					if count > 0 && count%100 == 0 {
						fmt.Printf("Updated %d users...\n", count)
					}
				}
				fmt.Printf("Updated %d users in Neo4j.\n", count)

				// 3.5 Extended Graph Analysis (Max Parity)
				fmt.Println("Running extended graph analysis (this may take a moment)...")

				// A. Groups Cracked %
				if grpStats, err := graphCli.GetGroupStats(); err == nil {
					// Convert graph.GroupStat to audit.GroupStat
					for _, g := range grpStats {
						stats.GroupsByPercentage = append(stats.GroupsByPercentage, audit.GroupStat{
							GroupName:    g.GroupName,
							Count:        g.Count,
							TotalMembers: g.TotalMembers,
							Percent:      g.Percent,
							Users:        g.Users,
						})
					}
					// Add summary entry
					stats.ReportEntries = append(stats.ReportEntries, audit.ReportEntry{
						Description: "Groups Cracked by Percentage",
						Count:       len(grpStats),
						HasDetails:  len(grpStats) > 0,
					})
				} else {
					fmt.Printf("Error querying group stats: %v\n", err)
				}

				// Define queries
				queries := []struct {
					Desc  string
					Query string
				}{
					{"All User Accounts", "MATCH (u:User) RETURN u.name"},
					{"All User Accounts Cracked", "MATCH (u:User {owned:true}) RETURN u.name"},
					{"Enabled User Accounts Cracked", "MATCH (u:User {owned:true, enabled:true}) RETURN u.name"},
					{"High Value User Accounts Cracked", "MATCH (u:User {owned:true, highvalue:true}) RETURN u.name"},

					{"Domain Admin Members", "MATCH (u:User) MATCH (g:Group) WHERE toUpper(g.name) CONTAINS 'DOMAIN ADMINS' MATCH (u)-[:MemberOf*1..]->(g) RETURN distinct u.name"},
					{"Domain Admin Members Cracked", "MATCH (u:User {owned:true}) MATCH (g:Group) WHERE toUpper(g.name) CONTAINS 'DOMAIN ADMINS' MATCH (u)-[:MemberOf*1..]->(g) RETURN distinct u.name"},

					{"Enterprise Admin Members", "MATCH (u:User) MATCH (g:Group) WHERE toUpper(g.name) CONTAINS 'ENTERPRISE ADMINS' MATCH (u)-[:MemberOf*1..]->(g) RETURN distinct u.name"},
					{"Enterprise Admin Members Cracked", "MATCH (u:User {owned:true}) MATCH (g:Group) WHERE toUpper(g.name) CONTAINS 'ENTERPRISE ADMINS' MATCH (u)-[:MemberOf*1..]->(g) RETURN distinct u.name"},

					{"Administrator Group Members", "MATCH (u:User) MATCH (g:Group) WHERE toUpper(g.name) CONTAINS 'ADMINISTRATORS' MATCH (u)-[:MemberOf*1..]->(g) RETURN distinct u.name"},
					{"Administrator Group Member Accounts Cracked", "MATCH (u:User {owned:true}) MATCH (g:Group) WHERE toUpper(g.name) CONTAINS 'ADMINISTRATORS' MATCH (u)-[:MemberOf*1..]->(g) RETURN distinct u.name"},

					{"Kerberoastable Users Cracked", "MATCH (u:User {owned:true, hasspn:true}) RETURN u.name"},
					{"Accounts Not Requiring Kerberos Pre-Authentication Cracked", "MATCH (u:User {owned:true, dontreqpreauth:true}) RETURN u.name"},
					{"Unconstrained Delegation Accounts Cracked", "MATCH (u:User {owned:true, unconstraineddelegation:true}) RETURN u.name"},

					// Timestamps: Neo4j uses epoch seconds.
					// 6 months = 15778800 seconds
					// 1 year = 31557600 seconds
					{"Inactive Accounts (Last Used Over 6mos Ago) Cracked", "MATCH (u:User {owned:true}) WHERE u.lastlogontimestamp < (timestamp()/1000 - 15778800) RETURN u.name"},
					{"Accounts With Passwords Set Over 1yr Ago Cracked", "MATCH (u:User {owned:true}) WHERE u.pwdlastset < (timestamp()/1000 - 31557600) RETURN u.name"},
					{"Accounts With Passwords That Never Expire Cracked", "MATCH (u:User {owned:true, pwdneverexpires:true}) RETURN u.name"},

					{"Accounts With Paths To Unconstrained Delegation Objects Cracked (Excluding DCs)", "MATCH (u:User {owned:true}) MATCH (c:Computer {unconstraineddelegation:true}) WHERE NOT toUpper(c.distinguishedname) CONTAINS 'DOMAIN CONTROLLERS' MATCH p=shortestPath((u)-[*1..]->(c)) RETURN distinct u.name"},
					{"Accounts With Paths To High Value Targets Cracked", "MATCH (u:User {owned:true}) MATCH (t {highvalue:true}) MATCH p=shortestPath((u)-[*1..]->(t)) RETURN distinct u.name"},

					{"Accounts With Explicit Admin Rights Cracked", "MATCH (u:User {owned:true})-[r:AdminTo]->(c:Computer) RETURN distinct u.name"},
					{"Accounts With Group Delegated Admin Rights Cracked", "MATCH (u:User {owned:true}) MATCH (g:Group) MATCH (u)-[:MemberOf*1..]->(g) MATCH (g)-[r:AdminTo]->(c:Computer) RETURN distinct u.name"},

					{"Accounts With Explicit Controlling Privileges Cracked", "MATCH (u:User {owned:true})-[r]->(t) WHERE type(r) IN ['ForceChangePassword', 'AddMember', 'GenericAll', 'GenericWrite', 'WriteDacl', 'WriteOwner'] RETURN distinct u.name"},
					{"Accounts With Group Delegated Controlling Privileges Cracked", "MATCH (u:User {owned:true}) MATCH (g:Group) MATCH (u)-[:MemberOf*1..]->(g) MATCH (g)-[r]->(t) WHERE type(r) IN ['ForceChangePassword', 'AddMember', 'GenericAll', 'GenericWrite', 'WriteDacl', 'WriteOwner'] RETURN distinct u.name"},
				}

				for _, q := range queries {
					users, err := graphCli.GetUsers(q.Query, nil)
					if err != nil {
						fmt.Printf("Query error [%s]: %v\n", q.Desc, err)
						continue
					}
					// Sort users alphabetically
					sort.Strings(users)
					stats.ReportEntries = append(stats.ReportEntries, audit.ReportEntry{
						Description: q.Desc,
						Count:       len(users),
						Users:       users,
						HasDetails:  len(users) > 0,
					})
				}
				fmt.Printf("Analysis complete. Generated %d report entries.\n", len(stats.ReportEntries))

				// 4. Report
				reportPath := filepath.Join(workingDir, fmt.Sprintf("AuditReport_%s.html", time.Now().Format("20060102_150405")))
				fmt.Printf("Generating report at %s...\n", reportPath)
				if err := report.Generate(reportPath, stats, *auditTemplate); err != nil {
					fmt.Printf("Error generating report: %v\n", err)
				} else {
					fmt.Println("Report generated successfully!")
				}
				return
			}
		}
	} else if *auditNTDS != "" || *auditCracked != "" {
		fmt.Println("Warning: Both -audit-ntds and -audit-cracked are required for auditing.")
	}

	fmt.Printf("\nSiloHound is now running in the background.\n")
	fmt.Printf("URL: http://127.0.0.1:8181\n")
	fmt.Printf("User: admin\nPass: admin\n\n")
	fmt.Printf("To stop the containers, run:\n")
	fmt.Printf("  %s -name %s -stop\n", os.Args[0], *name)
}

func createFolders(base string) {
	os.MkdirAll(filepath.Join(base, "bloodhound-data", "postgresql"), 0755)
	os.MkdirAll(filepath.Join(base, "bloodhound-data", "neo4j"), 0755)
}

func injectQueries(mgr *docker.Manager, psqlID string, queries importer.BloodHoundQueries) {
	// Check database connectivity. Note: We can't easily capture the query result
	// to verify the admin user exists, but the INSERT queries below have WHERE EXISTS
	// clauses that will prevent insertion if the admin user doesn't exist.
	checkSQL := "SELECT COUNT(*) FROM users WHERE principal_name = 'admin';"
	checkCmd := []string{"psql", "-t", "-U", "bloodhound", "-d", "bloodhound", "-c", checkSQL}
	
	if err := mgr.Exec(psqlID, checkCmd); err != nil {
		fmt.Printf("Warning: Unable to connect to database: %v\n", err)
		fmt.Println("Note: Queries can only be injected after the admin user is created.")
		fmt.Println("Please log in to BloodHound UI first, then re-run with the -custom flag.")
		return
	}
	
	for i, q := range queries.Queries {
		fmt.Printf("Injecting [%d/%d]: %s\n", i+1, len(queries.Queries), q.Name)

		sName := escapeSQL(q.Name)
		sQuery := escapeSQL(q.Query)
		sDesc := escapeSQL(q.Description)

		// SQL to insert query
		sqlQuery := fmt.Sprintf(
			"INSERT INTO saved_queries (user_id, name, query, description) SELECT (SELECT id FROM users WHERE principal_name = 'admin'), '%s', '%s', '%s' WHERE EXISTS (SELECT 1 FROM users WHERE principal_name = 'admin') AND NOT EXISTS (SELECT 1 FROM saved_queries WHERE name = '%s');",
			sName, sQuery, sDesc, sName,
		)

		cmd := []string{"psql", "-q", "-U", "bloodhound", "-d", "bloodhound", "-c", sqlQuery}

		if err := mgr.Exec(psqlID, cmd); err != nil {
			fmt.Printf("Failed to inject query '%s': %v\n", q.Name, err)
		}
	}
	
	fmt.Printf("\nQuery injection complete. Processed %d queries.\n", len(queries.Queries))
	fmt.Println("Note: If queries don't appear in BloodHound, make sure you've logged in to the UI first.")
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
