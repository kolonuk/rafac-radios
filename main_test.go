package main

import (
	"testing"
)

func TestGetOrCreateLesson(t *testing.T) {
	l1 := getOrCreateLesson("L1")
	if l1 == nil {
		t.Fatal("Expected lesson L1 to be created")
	}
	if l1.ID != "L1" {
		t.Errorf("Expected lesson ID L1, got %s", l1.ID)
	}

	l2 := getOrCreateLesson("L1")
	if l1 != l2 {
		t.Error("Expected same lesson instance for same ID")
	}
}

func TestLessonStudentsAndCallsigns(t *testing.T) {
	l := getOrCreateLesson("L2")
	l.mu.Lock()
	l.Students["s1"] = &Student{ID: "s1", Name: "Alice"}
	l.Callsigns = append(l.Callsigns, "Alpha")
	l.mu.Unlock()

	l = getOrCreateLesson("L2")
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
