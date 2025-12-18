package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Project struct {
	ID        int
	Name      string
	Path      string
	CreatedAt time.Time
}

type Database struct {
	db *sql.DB
}

func InitDB() (*Database, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dbPath := filepath.Join(home, ".silohound", "projects.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		return nil, err
	}

	return &Database{db: db}, nil
}

func migrate(db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS projects (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		path TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	`
	_, err := db.Exec(query)
	return err
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) AddProject(name, path string) error {
	_, err := d.db.Exec("INSERT INTO projects (name, path) VALUES (?, ?)", name, path)
	return err
}

func (d *Database) GetProject(name string) (*Project, error) {
	row := d.db.QueryRow("SELECT id, name, path, created_at FROM projects WHERE name = ?", name)
	var p Project
	err := row.Scan(&p.ID, &p.Name, &p.Path, &p.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (d *Database) ListProjects() ([]Project, error) {
	rows, err := d.db.Query("SELECT id, name, path, created_at FROM projects")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Path, &p.CreatedAt); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func (d *Database) DeleteProject(name string) error {
	_, err := d.db.Exec("DELETE FROM projects WHERE name = ?", name)
	return err
}

func (d *Database) UpdateProjectPath(name, newPath string) error {
	_, err := d.db.Exec("UPDATE projects SET path = ? WHERE name = ?", newPath, name)
	return err
}
