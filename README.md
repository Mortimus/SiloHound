# SiloHound

SiloHound (formerly ProjectBloodHound) is a tool designed to streamline the management of **BloodHound Community Edition (CE)** projects. It allows security professionals to easily spin up isolated, project-specific BloodHound environments using Docker. 

All instances are pre-configured with `admin:admin` credentials (password expiration set to 1 year) and isolated networking, ensuring smooth operation for multiple concurrent assessments.

## Features

- **Project Management**: 
  - Create, List, Resume, and Delete projects.
  - **Resume Capability**: Automatically finds previous data paths for known projects.
  - **Move Support**: update project paths in the database if folders are moved.
  - **Safety**: Prevents overwriting existing projects with path safety checks.
- **Docker Integration**: 
  - Fully automated container orchestration using the Docker SDK.
  - **Namespacing**: Unique container and network names per project (e.g., `SiloHound_ProjectName_Neo4j`).
  - **Stop Command**: dedicated flag to cleanly stop all containers for a specific project.
- **Password Auditing**: 
  - Integrated NTLM password auditing.
  - Correlates `secretsdump` output with cracked hashes.
  - Updates Neo4j graph with `owned`, `password`, `cracked`, and `nthash` properties.
  - Generates detailed HTML reports with statistics (Reuse, Length, Complexity).
- **Query Management**: 
  - Inject custom Cypher queries from a JSON file.
  - Built-in support to clone and inject the SpecterOps BloodHound Query Library.
- **Developer Friendly**: Written in Go with SQLite persistence for project tracking.

## Requirements

*   **Docker**: The Docker daemon must be installed and running.
*   **Go** (Optional): For building from source (Go 1.23+ recommended).

## Installation

```bash
# Install directly via Go
go install github.com/Mortimus/SiloHound@latest
```

Alternatively, to build from source:

```bash
# Clone the repository
git clone https://github.com/Mortimus/SiloHound.git

# Build the binary
cd SiloHound
go build -o silohound .

## Usage

### Basic Project Management

```bash
# Start a new project (or resume existing)
silohound -name "Assessment2025" -path ./data/client_a

# List all tracked projects
silohound -list

# Stop containers for a specific project
silohound -name "Assessment2025" -stop

# Remove a project (stops containers and removes from DB)
silohound -clean -name "Assessment2025"

# Move a project to a new location (updates DB record only)
silohound -name "Assessment2025" -move /new/path/to/data
```

### Accessing the Instance
*   **BloodHound UI**: [http://127.0.0.1:8181](http://127.0.0.1:8181)
*   **Neo4j Browser**: [http://127.0.0.1:7474](http://127.0.0.1:7474)
*   **Default Credentials**: `admin` / `admin`

### Password Auditing
SiloHound can ingest `secretsdump` NTDS output and a list of cracked hashes (e.g., from Hashcat/John) to enrich the graph and generate reports.

```bash
silohound -name "Assessment2025" \
  -audit-ntds ./ntds.secretsdump \
  -audit-cracked ./cracked.txt
```
*   **-audit-ntds**: Path to file formatted as `user:id:lm:nt:::`.
*   **-audit-cracked**: Path to file formatted as `hash:cleartext`.

### Query Injection

```bash
# Inject local custom queries
silohound -name "Assessment2025" -custom ./my_queries.json

# Clone and inject the official SpecterOps Query Library
silohound -name "Assessment2025" -clone-queries
```

## Architecture & Data
*   **Database**: Projects are tracked in `~/.silohound/projects.db` (SQLite).
*   **Project Data**: Each project creates a `bloodhound-data` folder in its specified path containing `postgresql` and `neo4j` subdirectories.
*   **Logs**: Containers stream logs to stdout/stderr.

## License
MIT
