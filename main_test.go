package main

import (
	"testing"
)

func TestLessonLifecycle(t *testing.T) {
	// Initially should be nil
	l := getLesson("L1")
	if l != nil {
		t.Fatal("Expected lesson L1 to be nil initially")
	}

	// Tutor creates lesson
	l1 := createLesson("L1", "admin")
	if l1 == nil {
		t.Fatal("Expected lesson L1 to be created")
	}
	if !l1.IsActive {
		t.Error("Expected lesson to be active")
	}

	// Now getLesson should work
	l2 := getLesson("L1")
	if l1 != l2 {
		t.Error("Expected same lesson instance")
	}

	// End lesson
	l1.IsActive = false
	l3 := getLesson("L1")
	if l3 != nil {
		t.Error("Expected lesson to be nil after being deactivated")
	}
}

func TestLessonStudentsAndCallsigns(t *testing.T) {
	l := createLesson("L2", "admin")
	l.mu.Lock()
	l.Students["s1"] = &Student{ID: "s1", Name: "Alice"}
	l.Callsigns = append(l.Callsigns, "Alpha")
	l.mu.Unlock()

	l = getLesson("L2")
	l.mu.Lock()
	if len(l.Students) != 1 {
		t.Errorf("Expected 1 student, got %d", len(l.Students))
	}
	if l.Students["s1"].Name != "Alice" {
		t.Errorf("Expected student name Alice, got %s", l.Students["s1"].Name)
	}
	if len(l.Callsigns) != 1 || l.Callsigns[0] != "Alpha" {
		t.Error("Expected callsign Alpha")
	}
	l.mu.Unlock()
}
