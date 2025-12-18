package database

import (
	"os"
	"testing"
)

func TestDatabase_Projects(t *testing.T) {
	// Setup Temp DB
	tmpDir, err := os.MkdirTemp("", "silohound_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Override InitDB logic slightly by manually opening DB in temp path
	// But InitDB uses hardcoded path relative to home.
	// Easier to just use the migrate function if I export it or access internal fields?
	// migrate is private.
	// But InitDB calls os.UserHomeDir(). We can't easily mock that without dependency injection.
	// Let's modify InitDB to take a path or make a helper for testing?
	// Or just test the methods on a struct if we can construct it.
	// I can manually construct Database and call private migrate? No, private.
	// I should probably skip InitDB test specifically or refactor it.
	// For now, let's verify if I can "fake" the home dir env var?

	origHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", origHome)

	db, err := InitDB()
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Test Add
	err = db.AddProject("TestProj", "/tmp/path")
	if err != nil {
		t.Errorf("AddProject failed: %v", err)
	}

	// Test Get
	p, err := db.GetProject("TestProj")
	if err != nil {
		t.Errorf("GetProject failed: %v", err)
	}
	if p == nil {
		t.Fatal("Project not found")
	}
	if p.Name != "TestProj" || p.Path != "/tmp/path" {
		t.Errorf("Project mismatch: %+v", p)
	}

	// Test Update
	err = db.UpdateProjectPath("TestProj", "/tmp/newpath")
	if err != nil {
		t.Errorf("UpdateProjectPath failed: %v", err)
	}
	p, _ = db.GetProject("TestProj")
	if p.Path != "/tmp/newpath" {
		t.Errorf("Path not updated, got %s", p.Path)
	}

	// Test List
	list, err := db.ListProjects()
	if err != nil {
		t.Errorf("ListProjects failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("Expected 1 project, got %d", len(list))
	}

	// Test Delete
	err = db.DeleteProject("TestProj")
	if err != nil {
		t.Errorf("DeleteProject failed: %v", err)
	}
	p, _ = db.GetProject("TestProj")
	if p != nil {
		t.Errorf("Project should be nil after delete, got %+v", p)
	}
}
