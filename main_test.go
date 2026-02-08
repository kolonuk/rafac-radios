package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSecurityManager(t *testing.T) {
	sm = &SecurityManager{Stats: make(map[string]*IPStats)}
	ip := "1.2.3.4"

	// 1. Test 5 failures trigger temporary block
	for i := 0; i < 5; i++ {
		sm.RecordAttempt(ip, false)
	}

	blocked, msg := sm.IsBlocked(ip)
	if !blocked {
		t.Error("Expected IP to be blocked after 5 failures")
	}
	if sm.Stats[ip].BlockStatus != BlockTemp {
		t.Errorf("Expected temporary block, got %s", sm.Stats[ip].BlockStatus)
	}
	t.Logf("Block message: %s", msg)

	// 2. Test auto-unblock after 1 minute (simulated)
	sm.mu.Lock()
	sm.Stats[ip].LastAttempt = time.Now().Add(-2 * time.Minute)
	sm.mu.Unlock()

	blocked, _ = sm.IsBlocked(ip)
	if blocked {
		t.Error("Expected IP to be unblocked after 1 minute has passed")
	}

	// 3. Test 20 failures trigger permanent block
	for i := 0; i < 20; i++ {
		sm.RecordAttempt(ip, false)
	}

	blocked, msg = sm.IsBlocked(ip)
	if !blocked {
		t.Error("Expected IP to be blocked after 20 failures")
	}
	if sm.Stats[ip].BlockStatus != BlockPerm {
		t.Errorf("Expected permanent block, got %s", sm.Stats[ip].BlockStatus)
	}
	t.Logf("Permanent block message: %s", msg)
}

func TestAdminAccessControl(t *testing.T) {
	systemCodeEnv = "secret_code"

	// 1. Unauthorized request
	req1 := httptest.NewRequest("GET", "/admin", nil)
	if isAdmin(req1) {
		t.Error("Expected isAdmin to be false for request without cookie")
	}

	// 2. Request with wrong code
	req2 := httptest.NewRequest("GET", "/admin", nil)
	req2.AddCookie(&http.Cookie{Name: "admin_access", Value: "wrong_code"})
	if isAdmin(req2) {
		t.Error("Expected isAdmin to be false for request with wrong cookie value")
	}

	// 3. Authorized request
	req3 := httptest.NewRequest("GET", "/admin", nil)
	req3.AddCookie(&http.Cookie{Name: "admin_access", Value: "secret_code"})
	if !isAdmin(req3) {
		t.Error("Expected isAdmin to be true for request with correct cookie value")
	}
}

func TestLessonLifecycle(t *testing.T) {
	lessonsMu.Lock()
	lessons = make(map[string]*Lesson)
	lessonsMu.Unlock()

	l := getLesson("L1")
	if l != nil {
		t.Fatal("Expected lesson L1 to be nil initially")
	}

	l1 := createLesson("L1", "admin", "Tutor Alice")
	if l1 == nil {
		t.Fatal("Expected lesson L1 to be created")
	}
	if !l1.IsActive {
		t.Error("Expected lesson to be active")
	}
	if l1.Tutors[l1.MainTutorID] != "Tutor Alice" {
		t.Errorf("Expected TutorName Tutor Alice, got %s", l1.Tutors[l1.MainTutorID])
	}

	l2 := getLesson("L1")
	if l1 != l2 {
		t.Error("Expected same lesson instance")
	}

	l1.IsActive = false
	l3 := getLesson("L1")
	if l3 != nil {
		t.Error("Expected lesson to be nil after being deactivated")
	}
}

func TestLessonIsolation(t *testing.T) {
	lessonsMu.Lock()
	lessons = make(map[string]*Lesson)
	lessonsMu.Unlock()

	l1 := createLesson("L1", "admin", "Tutor 1")
	l2 := createLesson("L2", "admin", "Tutor 2")

	l1.mu.Lock()
	l1.Students["s1"] = &Student{ID: "s1", Name: "Alice", Frequency: "100MHz", Callsign: "C1"}
	l1.mu.Unlock()

	l2.mu.Lock()
	l2.Students["s2"] = &Student{ID: "s2", Name: "Bob", Frequency: "200MHz", Callsign: "C2"}
	l2.mu.Unlock()

	l2.mu.Lock()
	if _, ok := l2.Students["s1"]; ok {
		t.Error("Student s1 from L1 found in L2")
	}
	l2.mu.Unlock()

	if l1.Students["s1"].Frequency != "100MHz" {
		t.Errorf("L1 Student s1 frequency changed unexpectedly")
	}
	if l2.Students["s2"].Frequency != "200MHz" {
		t.Errorf("L2 Student s2 frequency changed unexpectedly")
	}
}

func TestCallsignUniqueness(t *testing.T) {
	lessonsMu.Lock()
	lessons = make(map[string]*Lesson)
	lessonsMu.Unlock()

	l := createLesson("L1", "admin", "Tutor")
	l.mu.Lock()
	l.Students["s1"] = &Student{ID: "s1", Name: "Alice", Callsign: "Alpha"}
	l.Students["s2"] = &Student{ID: "s2", Name: "Bob"}
	l.mu.Unlock()

	assignCallsign := func(lesson *Lesson, studentID string, callsign string) bool {
		lesson.mu.Lock()
		defer lesson.mu.Unlock()
		unique := true
		for _, s := range lesson.Students {
			if s.Callsign == callsign && s.ID != studentID {
				unique = false
				break
			}
		}
		if unique {
			if s, ok := lesson.Students[studentID]; ok {
				s.Callsign = callsign
				return true
			}
		}
		return false
	}

	if assignCallsign(l, "s2", "Alpha") {
		t.Error("Allowed non-unique callsign Alpha in same lesson")
	}
	if !assignCallsign(l, "s2", "Beta") {
		t.Error("Failed to assign unique callsign Beta")
	}
}
